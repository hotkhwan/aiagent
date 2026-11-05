// internal/aiagent/messages.go
//
// Conversation view: the user/assistant TEXT of a session, read from the tail
// of its transcript. Tool inputs/outputs, system reminders and thinking are
// dropped, and secrets are redacted before anything leaves the host.
package world

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Message is one conversational turn.
type Message struct {
	Role string    `json:"role"` // user | assistant
	At   time.Time `json:"at"`
	Text string    `json:"text"`
}

const (
	messageTailBytes = 3 << 20
	messageMaxRunes  = 6000
)

var redactions = []struct {
	re   *regexp.Regexp
	with string
}{
	{regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*?-----END [A-Z ]*PRIVATE KEY-----`), "«private key redacted»"},
	{regexp.MustCompile(`\bkaw_[A-Za-z0-9_-]{20,}`), "«aiagent token redacted»"},
	{regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{20,}`), "«github token redacted»"},
	{regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{20,}`), "«github token redacted»"},
	{regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{20,}`), "«api key redacted»"},
	{regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`), "«aws key redacted»"},
	{regexp.MustCompile(`\bxox[abprs]-[A-Za-z0-9-]{10,}`), "«slack token redacted»"},
	{regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}`), "«jwt redacted»"},
	{regexp.MustCompile(`(?i)\b(bearer)\s+[A-Za-z0-9._~+/-]{20,}=*`), "$1 «redacted»"},
	{regexp.MustCompile(`\b[A-Z0-9]{4}(?:-[A-Z0-9]{4}){7}\b`), "«webinit code redacted»"},
	{regexp.MustCompile(`(?i)\b(password|passwd|secret|api[_-]?key|token)(\s*[:=]\s*)("[^"\s]{6,}"|'[^'\s]{6,}'|[^\s"',;]{6,})`), "$1$2«redacted»"},
	{regexp.MustCompile(`(?i)(https?://)[^/\s:@]+:[^/\s@]+@`), "$1«credentials redacted»@"},
}

// Redact removes known secret shapes from free text.
func Redact(s string) string {
	for _, r := range redactions {
		s = r.re.ReplaceAllString(s, r.with)
	}
	return s
}

var (
	reminderRE  = regexp.MustCompile(`(?s)<system-reminder>.*?</system-reminder>`)
	bashInRE    = regexp.MustCompile(`(?s)<bash-input>(.*?)</bash-input>`)
	internalRE  = regexp.MustCompile(`(?s)<(bash-stdout|bash-stderr|task-notification|local-command-stdout|local-command-stderr|command-message|command-args)>.*?</(bash-stdout|bash-stderr|task-notification|local-command-stdout|local-command-stderr|command-message|command-args)>`)
	commandName = regexp.MustCompile(`(?s)<command-name>(.*?)</command-name>`)
)

func cleanTurn(s string) string {
	s = reminderRE.ReplaceAllString(s, "")
	s = bashInRE.ReplaceAllString(s, "! $1")
	s = commandName.ReplaceAllString(s, "$1")
	s = internalRE.ReplaceAllString(s, "")
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "<command-") || strings.HasPrefix(s, "<local-command") || strings.HasPrefix(s, "<environment_context>") || strings.HasPrefix(s, "<user_instructions>") || strings.HasPrefix(s, "Caveat:") || strings.HasPrefix(s, "This session is being continued from a previous conversation") {
		return ""
	}
	return truncateRunes(Redact(s), messageMaxRunes)
}

// Messages returns the last `limit` conversational turns of a session.
func (c *Collector) Messages(ctx context.Context, id string, limit int) ([]Message, error) {
	d, err := c.Detail(ctx, id)
	if err != nil {
		return nil, err
	}
	path := d.Session.sourcePath
	if path == "" {
		return []Message{}, nil
	}
	fi, err := os.Stat(path)
	if err != nil {
		return []Message{}, nil
	}
	// same file, same size, same mtime → same answer (monitor polls often)
	ck := msgCacheKey{path, fi.Size(), fi.ModTime().UnixNano(), limit}
	msgCache.mu.Lock()
	if v, ok := msgCache.m[ck]; ok {
		msgCache.mu.Unlock()
		return append([]Message(nil), v...), nil
	}
	msgCache.mu.Unlock()
	// transcripts can end in megabytes of tool output (e.g. screenshots);
	// start small for short requests and widen until enough turns are found
	start := int64(messageTailBytes)
	if limit <= 10 {
		start = 128 << 10
	}
	var out []Message
	for window := start; ; window *= 4 {
		lines := readTail(path, fi.Size(), window)
		if d.Session.RuntimeType == "codexCli" {
			out = codexMessages(lines)
		} else {
			out = claudeMessages(lines)
		}
		if len(out) >= limit || window >= fi.Size() || window >= 64<<20 {
			break
		}
	}
	if len(out) > limit {
		out = out[len(out)-limit:]
	}
	if out == nil {
		out = []Message{}
	}
	msgCache.mu.Lock()
	if len(msgCache.m) > 256 {
		msgCache.m = map[msgCacheKey][]Message{}
	}
	msgCache.m[ck] = out
	msgCache.mu.Unlock()
	return append([]Message(nil), out...), nil
}

type msgCacheKey struct {
	path  string
	size  int64
	mtime int64
	limit int
}

var msgCache = struct {
	mu sync.Mutex
	m  map[msgCacheKey][]Message
}{m: map[msgCacheKey][]Message{}}

func claudeMessages(lines [][]byte) []Message { return claudeTurns(lines, false) }

// claudeTurns parses transcript lines; sidechain lines (a sub-agent's own
// transcript) are kept only when side is true.
func claudeTurns(lines [][]byte, side bool) []Message {
	var out []Message
	var lastID string
	for _, raw := range lines {
		if !bytes.Contains(raw, []byte(`"type":"user"`)) && !bytes.Contains(raw, []byte(`"type":"assistant"`)) {
			continue
		}
		var ln struct {
			Type        string `json:"type"`
			Timestamp   string `json:"timestamp"`
			IsMeta      bool   `json:"isMeta"`
			IsSidechain bool   `json:"isSidechain"`
			Message     *struct {
				ID      string          `json:"id"`
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(raw, &ln) != nil || ln.Message == nil || ln.IsMeta || (ln.IsSidechain && !side) {
			continue
		}
		var text string
		var str string
		if json.Unmarshal(ln.Message.Content, &str) == nil {
			text = str
		} else {
			var blocks []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			}
			if json.Unmarshal(ln.Message.Content, &blocks) != nil {
				continue
			}
			var parts []string
			for _, b := range blocks {
				if b.Type == "text" && b.Text != "" {
					parts = append(parts, b.Text)
				}
			}
			text = strings.Join(parts, "\n")
		}
		if text = cleanTurn(text); text == "" {
			continue
		}
		role := "assistant"
		if ln.Type == "user" {
			role = "user"
		}
		// one streamed assistant message can span several lines (same id);
		// separate messages in a turn (progress notes between tool calls)
		// stay separate so the latest one is never truncated away
		if n := len(out); n > 0 && role == "assistant" && ln.Message.ID != "" && lastID == ln.Message.ID {
			out[n-1].Text = truncateRunes(out[n-1].Text+"\n\n"+text, messageMaxRunes)
			continue
		}
		lastID = ln.Message.ID
		out = append(out, Message{Role: role, At: parseTS(ln.Timestamp), Text: text})
	}
	return out
}

func codexMessages(lines [][]byte) []Message {
	var out []Message
	for _, raw := range lines {
		var ln codexLine
		if len(raw) == 0 || json.Unmarshal(raw, &ln) != nil || ln.Type != "response_item" {
			continue
		}
		var pl struct {
			Type    string `json:"type"`
			Role    string `json:"role"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		}
		if json.Unmarshal(ln.Payload, &pl) != nil || pl.Type != "message" || (pl.Role != "user" && pl.Role != "assistant") {
			continue
		}
		var parts []string
		for _, b := range pl.Content {
			if b.Text != "" {
				parts = append(parts, b.Text)
			}
		}
		text := cleanTurn(strings.Join(parts, "\n"))
		if text == "" {
			continue
		}
		out = append(out, Message{Role: pl.Role, At: parseTS(ln.Timestamp), Text: text})
	}
	return out
}
