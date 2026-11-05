// internal/aiagent/claude.go
//
// Claude Code sources (read-only):
//
//	<home>/.claude/sessions/<pid>.json        live-session registry (pid, procStart,
//	                                         sessionId, cwd, name, status, startedAt)
//	<home>/.claude/projects/<slug>/<id>.jsonl  transcript (usage, tool steps, title,
//	                                         git branch, PR links)
//
// The registry's *.key siblings are secrets and are never opened.
package world

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type claudeRegistry struct {
	Pid       int    `json:"pid"`
	SessionID string `json:"sessionId"`
	Cwd       string `json:"cwd"`
	StartedAt int64  `json:"startedAt"`
	ProcStart string `json:"procStart"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	Status    string `json:"status"`
	UpdatedAt int64  `json:"updatedAt"`
}

type claudeLine struct {
	Type         string `json:"type"`
	Timestamp    string `json:"timestamp"`
	GitBranch    string `json:"gitBranch"`
	Cwd          string `json:"cwd"`
	AiTitle      string `json:"aiTitle"`
	PrNumber     int    `json:"prNumber"`
	PrURL        string `json:"prUrl"`
	PrRepository string `json:"prRepository"`
	Message      *struct {
		ID         string `json:"id"`
		StopReason string `json:"stop_reason"`
		Model      string `json:"model"`
		Usage      *struct {
			InputTokens   int64 `json:"input_tokens"`
			OutputTokens  int64 `json:"output_tokens"`
			CacheCreation int64 `json:"cache_creation_input_tokens"`
			CacheRead     int64 `json:"cache_read_input_tokens"`
		} `json:"usage"`
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

type contentBlock struct {
	Type  string          `json:"type"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

// transcriptState caches what has been learned from one transcript file so a
// refresh only reads what was appended.
type transcriptState struct {
	size    int64
	modTime time.Time

	// usage counting (incremental from offset 0, budgeted per refresh)
	offset    int64
	usage     Usage
	lastMsgID string

	// tail-derived
	title       string
	branch      string
	cwd         string
	lastTool    string
	activity    string
	lastStop    string
	lastEventAt time.Time
	prs         map[int]PRLink
	timeline    []TimelineItem
	watches     map[string]*watchState
	lastCI      *Watch
	model       string
}

const (
	tailBytes        = 768 << 10
	usageBudgetBytes = 64 << 20
	timelineMax      = 40
)

// readTail returns the complete lines in the last n bytes of a file.
func readTail(path string, size, n int64) [][]byte {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	start := size - n
	if start < 0 {
		start = 0
	}
	buf := make([]byte, size-start)
	if _, err := f.ReadAt(buf, start); err != nil && err != io.EOF {
		return nil
	}
	lines := bytes.Split(buf, []byte{'\n'})
	if start > 0 && len(lines) > 0 {
		lines = lines[1:] // first line is partial
	}
	return lines
}

func parseTS(s string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

var setModelRE = regexp.MustCompile("local-command-stdout>Set model to `([A-Za-z0-9 ._()-]{2,60})`")

// refreshClaudeTail re-derives title/activity/timeline from the file tail.
func refreshClaudeTail(path string, st *transcriptState) {
	if st.prs == nil {
		st.prs = map[int]PRLink{}
	}
	var timeline []TimelineItem
	lines := readTail(path, st.size, tailBytes)
	updateWatches(lines, st)
	for _, raw := range lines {
		if len(raw) == 0 {
			continue
		}
		if !bytes.Contains(raw, []byte(`"type":"assistant"`)) &&
			!bytes.Contains(raw, []byte(`"type":"ai-title"`)) &&
			!bytes.Contains(raw, []byte(`"type":"pr-link"`)) &&
			!bytes.Contains(raw, []byte(`"type":"user"`)) {
			continue
		}
		var ln claudeLine
		if json.Unmarshal(raw, &ln) != nil {
			continue
		}
		switch ln.Type {
		case "ai-title":
			if ln.AiTitle != "" {
				st.title = ln.AiTitle
			}
		case "pr-link":
			if ln.PrNumber > 0 && strings.HasPrefix(ln.PrURL, "https://") {
				st.prs[ln.PrNumber] = PRLink{Number: ln.PrNumber, URL: ln.PrURL, Repository: ln.PrRepository}
			}
		case "user", "assistant":
			if t := parseTS(ln.Timestamp); !t.IsZero() {
				st.lastEventAt = t
			}
			if ln.GitBranch != "" {
				st.branch = ln.GitBranch
			}
			if ln.Cwd != "" {
				st.cwd = ln.Cwd
			}
			// `/model X` typed in the CLI (or sent from the web) shows up as a
			// local-command stdout line before the next reply carries the id
			if ln.Type == "user" && bytes.Contains(raw, []byte("Set model to")) {
				if m := setModelRE.FindSubmatch(raw); m != nil {
					st.model = string(m[1])
				}
			}
			if ln.Type != "assistant" || ln.Message == nil {
				continue
			}
			if ln.Message.StopReason != "" {
				st.lastStop = ln.Message.StopReason
			}
			if m := ln.Message.Model; m != "" && !strings.HasPrefix(m, "<") {
				st.model = m
			}
			var blocks []contentBlock
			if json.Unmarshal(ln.Message.Content, &blocks) != nil {
				continue
			}
			for _, b := range blocks {
				if b.Type != "tool_use" || b.Name == "" {
					continue
				}
				var in struct {
					Command string `json:"command"`
				}
				_ = json.Unmarshal(b.Input, &in)
				act := classify(b.Name, in.Command)
				st.lastTool, st.activity = b.Name, act
				timeline = append(timeline, TimelineItem{At: parseTS(ln.Timestamp), Tool: b.Name, ActivityType: act})
			}
		}
	}
	if len(timeline) > 0 {
		if len(timeline) > timelineMax {
			timeline = timeline[len(timeline)-timelineMax:]
		}
		st.timeline = timeline
	}
}

// countClaudeUsage continues summing assistant usage from st.offset, reading
// at most usageBudgetBytes. Streamed lines of one message repeat the same
// usage under the same message id; only the first is counted.
func countClaudeUsage(path string, st *transcriptState) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	if _, err := f.Seek(st.offset, io.SeekStart); err != nil {
		return
	}
	r := bufio.NewReaderSize(f, 1<<20)
	var read int64
	for read < usageBudgetBytes {
		line, err := r.ReadBytes('\n')
		if err != nil {
			// incomplete trailing line: leave it for the next refresh
			break
		}
		read += int64(len(line))
		st.offset += int64(len(line))
		if !bytes.Contains(line, []byte(`"type":"assistant"`)) || !bytes.Contains(line, []byte(`"usage"`)) {
			continue
		}
		var ln claudeLine
		if json.Unmarshal(line, &ln) != nil || ln.Message == nil || ln.Message.Usage == nil {
			continue
		}
		if ln.Message.ID != "" && ln.Message.ID == st.lastMsgID {
			continue
		}
		st.lastMsgID = ln.Message.ID
		u := ln.Message.Usage
		st.usage.InputTokens += u.InputTokens + u.CacheCreation
		st.usage.OutputTokens += u.OutputTokens
		st.usage.CachedTokens += u.CacheRead
	}
	st.usage.Source = "claudeTranscript"
	st.usage.Complete = st.offset >= st.size
}

// collectClaude returns live sessions from the registry plus sessions whose
// transcript changed since `since` (ended, history only).
func (c *Collector) collectClaude(home, owner string, since time.Time) []Session {
	var out []Session
	liveIDs := map[string]bool{}
	regs, _ := filepath.Glob(filepath.Join(home, ".claude", "sessions", "*.json"))
	for _, p := range regs {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var reg claudeRegistry
		if json.Unmarshal(b, &reg) != nil || reg.SessionID == "" || reg.Pid <= 0 {
			continue
		}
		start, alive := procStartTime(c.ProcRoot, reg.Pid)
		if !alive || (reg.ProcStart != "" && start != reg.ProcStart) {
			continue // stale registry entry: the process is gone
		}
		liveIDs[reg.SessionID] = true
		s := Session{
			SessionID:    reg.SessionID,
			RuntimeType:  "claudeCode",
			ProviderID:   "anthropic",
			DisplayName:  reg.Name,
			Owner:        owner,
			Cwd:          reg.Cwd,
			RepoID:       repoID(reg.Cwd),
			Live:         true,
			LiveEvidence: "pid+procStart",
			Pid:          reg.Pid,
			Kind:         reg.Kind,
			home:         home,
			Status:       StatusRunning,
			ActivityType: ActivityUnknown,
		}
		if reg.Status == "idle" {
			s.Status, s.ActivityType = StatusIdle, ActivityIdle
		}
		if reg.StartedAt > 0 {
			t := time.UnixMilli(reg.StartedAt).UTC()
			s.StartedAt = &t
		}
		if tp := c.claudeTranscript(home, reg.SessionID); tp != "" {
			c.applyClaudeTranscript(tp, &s, true)
		}
		if s.DisplayName == "" {
			s.DisplayName = "Claude · " + s.RepoID
		}
		out = append(out, s)
	}
	// history: transcripts touched since `since` that no live process owns
	files, _ := filepath.Glob(filepath.Join(home, ".claude", "projects", "*", "*.jsonl"))
	for _, p := range files {
		id := strings.TrimSuffix(filepath.Base(p), ".jsonl")
		if liveIDs[id] {
			continue
		}
		fi, err := os.Stat(p)
		if err != nil || fi.ModTime().Before(since) {
			continue
		}
		s := Session{
			SessionID:    id,
			RuntimeType:  "claudeCode",
			ProviderID:   "anthropic",
			Owner:        owner,
			Status:       StatusEnded,
			LiveEvidence: "none",
			ActivityType: ActivityIdle,
			Usage:        Usage{Source: "notCounted"},
		}
		c.applyClaudeTranscript(p, &s, false)
		s.Cwd = cwdFromSlug(filepath.Base(filepath.Dir(p)), s.Cwd)
		s.RepoID = repoID(s.Cwd)
		s.DisplayName = "Claude · " + s.RepoID
		out = append(out, s)
	}
	return out
}

func (c *Collector) claudeTranscript(home, sessionID string) string {
	if p, ok := c.transcriptPath[sessionID]; ok {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	m, _ := filepath.Glob(filepath.Join(home, ".claude", "projects", "*", sessionID+".jsonl"))
	if len(m) == 0 {
		return ""
	}
	c.transcriptPath[sessionID] = m[0]
	return m[0]
}

func (c *Collector) applyClaudeTranscript(path string, s *Session, countUsage bool) {
	fi, err := os.Stat(path)
	if err != nil {
		return
	}
	st := c.transcripts[path]
	if st == nil {
		st = &transcriptState{}
		c.transcripts[path] = st
	}
	if fi.Size() < st.size {
		*st = transcriptState{} // truncated/rewritten: start over
	}
	if fi.Size() != st.size || !fi.ModTime().Equal(st.modTime) {
		st.size, st.modTime = fi.Size(), fi.ModTime()
		refreshClaudeTail(path, st)
	}
	if countUsage && st.offset < st.size {
		countClaudeUsage(path, st)
	}
	s.sourcePath = path
	if s.Cwd == "" {
		s.Cwd = st.cwd
	}
	s.Title = st.title
	s.GitBranch = st.branch
	s.LastTool = st.lastTool
	s.timeline = st.timeline
	s.Watches = activeWatches(st, c.now())
	if st.lastCI != nil && c.now().Sub(st.lastCI.EventAt) < lastCIKeep {
		w := *st.lastCI
		s.LastCI = &w
	}
	s.Model = st.model
	if !st.lastEventAt.IsZero() {
		t := st.lastEventAt
		s.LastEventAt = &t
	}
	if s.Status == StatusRunning && st.activity != "" {
		s.ActivityType = st.activity
	}
	if countUsage {
		s.Usage = st.usage
		s.SubAgents = c.subAgents(path, s.SessionID)
		s.Doing = c.jobDoing(s)
	}
	for _, pr := range st.prs {
		s.PRs = append(s.PRs, pr)
	}
}

// cwdFromSlug keeps a known cwd; the project slug is lossy (/ and . both
// become -), so it is only a last-resort label.
func cwdFromSlug(slug, known string) string {
	if known != "" {
		return known
	}
	return strings.ReplaceAll(slug, "-", "/")
}
