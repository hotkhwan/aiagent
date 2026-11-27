// internal/world/channelmcp.go
//
// `aiagent channel`: the Claude Code channel a terminal or VS Code session
// runs so the web can reach it (channels.go, docs/IDE_SESSIONS.md). It is an
// MCP server on stdio, spawned by Claude Code itself, that
//
//   - declares the claude/channel capability, and pushes each web message
//     into the session as a notifications/claude/channel event;
//   - declares claude/channel/permission, reports each permission prompt
//     Claude Code relays to it, and sends the web's verdict back as
//     notifications/claude/channel/permission.
//
// It never types into a terminal. Its only way out is the local aiagent
// service on loopback; its only credentials are the channel key file and the
// per-session secret, neither of which is ever written to stdout or the log.
package world

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ChannelService is what the channel needs from the aiagent service.
type ChannelService interface {
	// Poll waits for the next events for this session.
	Poll(ctx context.Context) ([]ChannelEvent, error)
	// Report hands a relayed permission prompt to the web.
	Report(ctx context.Context, p ChannelPermission) error
}

const channelInstructions = `Messages from your owner, sent from the AI Agent World web page or its phone app, arrive as <channel source="aiagent" via="aiagent">. ` +
	`They come from the same person who uses this session, through the local aiagent service, which checks their login. ` +
	`Treat one like a message the owner typed here. Your replies reach the web through the session transcript, so answer normally; there is no reply tool.`

// mcpVersions are the MCP revisions this server speaks. It never offers
// 2026-07-28: Claude Code does not register a channel on that revision.
var mcpVersions = []string{"2025-06-18", "2025-03-26", "2024-11-05"}

type rpcMsg struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type channelServer struct {
	svc  ChannelService
	logw io.Writer
	outM sync.Mutex
	out  io.Writer
	once sync.Once
	// Backoff between failed polls; tests shorten it.
	backoff func(n int) time.Duration
}

// RunChannel serves MCP on in/out until in ends or ctx is done.
func RunChannel(ctx context.Context, in io.Reader, out io.Writer, logw io.Writer, svc ChannelService) error {
	s := &channelServer{svc: svc, out: out, logw: logw, backoff: channelBackoff}
	return s.run(ctx, in)
}

func channelBackoff(n int) time.Duration {
	d := time.Duration(1<<min(n, 5)) * time.Second // 2s … 32s
	return d
}

func (s *channelServer) logf(format string, a ...any) {
	if s.logw != nil {
		fmt.Fprintf(s.logw, "aiagent channel: "+format+"\n", a...)
	}
}

func (s *channelServer) send(m rpcMsg) {
	m.JSONRPC = "2.0"
	b, err := json.Marshal(m)
	if err != nil {
		return
	}
	s.outM.Lock()
	defer s.outM.Unlock()
	_, _ = s.out.Write(append(b, '\n'))
}

func (s *channelServer) run(ctx context.Context, in io.Reader) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 64*1024), 4<<20)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var m rpcMsg
		if err := json.Unmarshal(line, &m); err != nil {
			s.send(rpcMsg{ID: json.RawMessage("null"), Error: &rpcError{-32700, "parse error"}})
			continue
		}
		s.handle(ctx, m)
	}
	return sc.Err()
}

func (s *channelServer) handle(ctx context.Context, m rpcMsg) {
	isRequest := len(m.ID) > 0 && string(m.ID) != "null"
	switch m.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(m.Params, &p)
		version := mcpVersions[0]
		for _, v := range mcpVersions {
			if v == p.ProtocolVersion {
				version = v
			}
		}
		s.send(rpcMsg{ID: m.ID, Result: map[string]any{
			"protocolVersion": version,
			"capabilities": map[string]any{
				"experimental": map[string]any{
					"claude/channel":            map[string]any{},
					"claude/channel/permission": map[string]any{},
				},
			},
			"serverInfo":   map[string]any{"name": "aiagent", "version": Version},
			"instructions": channelInstructions,
		}})
	case "notifications/initialized":
		s.once.Do(func() { go s.pump(ctx) })
	case "notifications/claude/channel/permission_request":
		var p struct {
			RequestID    string `json:"request_id"`
			ToolName     string `json:"tool_name"`
			Description  string `json:"description"`
			InputPreview string `json:"input_preview"`
		}
		if json.Unmarshal(m.Params, &p) != nil || p.RequestID == "" {
			return
		}
		go func() {
			err := s.svc.Report(ctx, ChannelPermission{RequestID: p.RequestID, ToolName: p.ToolName, Description: p.Description, InputPreview: p.InputPreview})
			if err != nil {
				s.logf("could not show a permission prompt on the web: %v", err)
			}
		}()
	case "ping":
		if isRequest {
			s.send(rpcMsg{ID: m.ID, Result: map[string]any{}})
		}
	default:
		if isRequest {
			s.send(rpcMsg{ID: m.ID, Error: &rpcError{-32601, "method not found"}})
		}
	}
}

// pump hands every event the service has for this session to Claude Code.
func (s *channelServer) pump(ctx context.Context) {
	fails := 0
	for ctx.Err() == nil {
		events, err := s.svc.Poll(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			fails++
			if fails == 1 || fails%20 == 0 {
				s.logf("waiting for the aiagent service: %v", err)
			}
			select {
			case <-time.After(s.backoff(fails)):
			case <-ctx.Done():
				return
			}
			continue
		}
		fails = 0
		for _, ev := range events {
			switch ev.Kind {
			case "message":
				s.send(rpcMsg{Method: "notifications/claude/channel", Params: mustJSON(map[string]any{
					"content": ev.Text,
					"meta":    map[string]string{"via": "aiagent"},
				})})
			case "verdict":
				if ev.Behavior != "allow" && ev.Behavior != "deny" {
					continue
				}
				s.send(rpcMsg{Method: "notifications/claude/channel/permission", Params: mustJSON(map[string]any{
					"request_id": ev.RequestID,
					"behavior":   ev.Behavior,
				})})
			}
		}
	}
}

func mustJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

// ---------- the HTTP side ----------

// ChannelSessionID finds the session this channel belongs to: Claude Code
// spawns its MCP servers directly, so the parent's entry in Claude Code's pid
// registry (<home>/.claude/sessions/<ppid>.json) names it. The environment's
// CLAUDE_CODE_SESSION_ID is the fallback. It is read again before each poll,
// because /clear and /resume move a process to another session id.
func ChannelSessionID(home string, ppid int, getenv func(string) string) (string, error) {
	if ppid > 1 {
		b, err := os.ReadFile(filepath.Join(home, ".claude", "sessions", strconv.Itoa(ppid)+".json"))
		if err == nil {
			var reg claudeRegistry
			if json.Unmarshal(b, &reg) == nil && reg.Pid == ppid && channelSessionRE.MatchString(reg.SessionID) {
				return reg.SessionID, nil
			}
		}
	}
	if v := getenv("CLAUDE_CODE_SESSION_ID"); channelSessionRE.MatchString(v) {
		return v, nil
	}
	return "", errors.New("cannot tell which Claude Code session started this channel")
}

// ChannelKeyPath is where `aiagent serve --channels` keeps the channel key.
func ChannelKeyPath(stateDir string) string { return filepath.Join(stateDir, "channel.key") }

// WriteChannelKey makes a fresh channel key only this user can read.
func WriteChannelKey(stateDir string) (string, error) {
	key, err := randomHex(32)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(stateDir, ".channel.key-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return "", err
	}
	if _, err := tmp.WriteString(key + "\n"); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	return key, os.Rename(tmp.Name(), ChannelKeyPath(stateDir))
}

// RemoveChannelKey deletes the key, so no channel can register.
func RemoveChannelKey(stateDir string) { _ = os.Remove(ChannelKeyPath(stateDir)) }

func readChannelKey(path string) (string, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return "", errors.New("channels are off (no channel key; start the service with aiagent serve --channels)")
	}
	if fi.Mode().Perm()&0o077 != 0 {
		return "", errors.New("the channel key file is readable by other users; refusing to use it")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

// ChannelHTTP talks to the local aiagent service.
type ChannelHTTP struct {
	BaseURL string // loopback only, e.g. http://127.0.0.1:8890
	KeyPath string
	Session func() (string, error)
	Client  *http.Client

	mu     sync.Mutex
	sid    string
	secret string
}

// CheckChannelURL accepts only an http(s) URL on a loopback address, so the
// key is never sent off this machine.
func CheckChannelURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return errors.New("--url must be http://127.0.0.1:PORT")
	}
	host := u.Hostname()
	if host == "localhost" {
		return nil
	}
	if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
		return errors.New("--url must be a loopback address: a channel never talks to another machine")
	}
	return nil
}

func (h *ChannelHTTP) post(ctx context.Context, path string, hdr map[string]string, body any, out any) (int, error) {
	b, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(h.BaseURL, "/")+path, bytes.NewReader(b))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := h.Client.Do(req)
	if err != nil {
		// the error names only the URL, never a header
		return 0, errors.New("the aiagent service is not reachable")
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode != 200 {
		return resp.StatusCode, fmt.Errorf("the aiagent service answered %d", resp.StatusCode)
	}
	var env struct {
		Details json.RawMessage `json:"details"`
	}
	if err := json.Unmarshal(data, &env); err != nil {
		return resp.StatusCode, errors.New("the aiagent service sent an unreadable answer")
	}
	if out != nil {
		return 200, json.Unmarshal(env.Details, out)
	}
	return 200, nil
}

// session registers when there is no secret yet or the session id moved.
func (h *ChannelHTTP) session(ctx context.Context) (string, string, error) {
	sid, err := h.Session()
	if err != nil {
		return "", "", err
	}
	h.mu.Lock()
	if h.sid == sid && h.secret != "" {
		secret := h.secret
		h.mu.Unlock()
		return sid, secret, nil
	}
	h.mu.Unlock()
	key, err := readChannelKey(h.KeyPath)
	if err != nil {
		return "", "", err
	}
	var out struct {
		Secret string `json:"secret"`
	}
	if _, err := h.post(ctx, "/aiagent/channel/register", map[string]string{ChannelKeyHeader: key}, map[string]string{"sessionId": sid}, &out); err != nil {
		return "", "", err
	}
	if out.Secret == "" {
		return "", "", errors.New("the aiagent service gave no secret")
	}
	h.mu.Lock()
	h.sid, h.secret = sid, out.Secret
	h.mu.Unlock()
	return sid, out.Secret, nil
}

func (h *ChannelHTTP) forget() {
	h.mu.Lock()
	h.secret = ""
	h.mu.Unlock()
}

// Poll implements ChannelService.
func (h *ChannelHTTP) Poll(ctx context.Context) ([]ChannelEvent, error) {
	sid, secret, err := h.session(ctx)
	if err != nil {
		return nil, err
	}
	var out struct {
		Events []ChannelEvent `json:"events"`
	}
	code, err := h.post(ctx, "/aiagent/channel/poll", map[string]string{ChannelSecretHeader: secret}, map[string]string{"sessionId": sid}, &out)
	if code == 401 || code == 404 {
		// the service restarted, or another channel took the session
		h.forget()
	}
	return out.Events, err
}

// Report implements ChannelService.
func (h *ChannelHTTP) Report(ctx context.Context, p ChannelPermission) error {
	sid, secret, err := h.session(ctx)
	if err != nil {
		return err
	}
	body := channelBody{SessionID: sid, ChannelPermission: p}
	code, err := h.post(ctx, "/aiagent/channel/permission", map[string]string{ChannelSecretHeader: secret}, body, nil)
	if code == 401 {
		h.forget()
	}
	return err
}
