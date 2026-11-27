package world

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
)

// mcpPeer is Claude Code's side of the channel's stdio.
type mcpPeer struct {
	t     *testing.T
	in    *io.PipeWriter
	lines chan rpcMsg
}

func startChannel(t *testing.T, svc ChannelService) *mcpPeer {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	p := &mcpPeer{t: t, in: inW, lines: make(chan rpcMsg, 32)}
	s := &channelServer{svc: svc, out: outW, backoff: func(int) time.Duration { return 10 * time.Millisecond }}
	go func() { _ = s.run(ctx, inR); outW.Close() }()
	go func() {
		sc := bufio.NewScanner(outR)
		for sc.Scan() {
			var m rpcMsg
			if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
				t.Errorf("channel wrote a line that is not JSON-RPC: %q", sc.Text())
				continue
			}
			p.lines <- m
		}
	}()
	t.Cleanup(func() { cancel(); inW.Close() })
	return p
}

func (p *mcpPeer) write(line string) {
	p.t.Helper()
	if _, err := p.in.Write([]byte(line + "\n")); err != nil {
		p.t.Fatal(err)
	}
}

func (p *mcpPeer) next() rpcMsg {
	p.t.Helper()
	select {
	case m := <-p.lines:
		return m
	case <-time.After(5 * time.Second):
		p.t.Fatal("the channel wrote nothing")
	}
	return rpcMsg{}
}

func (p *mcpPeer) handshake() {
	p.t.Helper()
	p.write(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2026-07-28","capabilities":{},"clientInfo":{"name":"claude-code","version":"2.1.325"}}}`)
	m := p.next()
	b, _ := json.Marshal(m.Result)
	var r struct {
		ProtocolVersion string `json:"protocolVersion"`
		Capabilities    struct {
			Experimental map[string]any `json:"experimental"`
		} `json:"capabilities"`
		Instructions string `json:"instructions"`
	}
	_ = json.Unmarshal(b, &r)
	if _, ok := r.Capabilities.Experimental["claude/channel"]; !ok {
		p.t.Fatalf("no claude/channel capability: %s", b)
	}
	if _, ok := r.Capabilities.Experimental["claude/channel/permission"]; !ok {
		p.t.Fatalf("no claude/channel/permission capability: %s", b)
	}
	// Claude Code does not register a channel on 2026-07-28
	if r.ProtocolVersion == "2026-07-28" || r.ProtocolVersion == "" {
		p.t.Fatalf("protocolVersion = %q", r.ProtocolVersion)
	}
	if r.Instructions == "" {
		p.t.Fatal("no instructions for Claude")
	}
	p.write(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
}

type fakeChannelService struct {
	mu      sync.Mutex
	events  chan []ChannelEvent
	reports []ChannelPermission
}

func (f *fakeChannelService) Poll(ctx context.Context) ([]ChannelEvent, error) {
	select {
	case ev := <-f.events:
		return ev, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (f *fakeChannelService) Report(_ context.Context, p ChannelPermission) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reports = append(f.reports, p)
	return nil
}

func TestChannelSpeaksTheChannelContract(t *testing.T) {
	f := &fakeChannelService{events: make(chan []ChannelEvent, 4)}
	p := startChannel(t, f)
	p.handshake()

	f.events <- []ChannelEvent{{Kind: "message", Text: "เพิ่ม test ให้ด้วย"}}
	m := p.next()
	if m.Method != "notifications/claude/channel" || len(m.ID) != 0 {
		t.Fatalf("got %+v, want a channel notification", m)
	}
	var cp struct {
		Content string            `json:"content"`
		Meta    map[string]string `json:"meta"`
	}
	_ = json.Unmarshal(m.Params, &cp)
	if cp.Content != "เพิ่ม test ให้ด้วย" || cp.Meta["via"] != "aiagent" {
		t.Fatalf("params = %s", m.Params)
	}

	f.events <- []ChannelEvent{{Kind: "verdict", RequestID: "abcde", Behavior: "allow"}, {Kind: "verdict", RequestID: "abcde", Behavior: "maybe"}}
	m = p.next()
	if m.Method != "notifications/claude/channel/permission" || string(m.Params) != `{"behavior":"allow","request_id":"abcde"}` {
		t.Fatalf("verdict = %s %s", m.Method, m.Params)
	}

	p.write(`{"jsonrpc":"2.0","method":"notifications/claude/channel/permission_request","params":{"request_id":"qwert","tool_name":"Bash","description":"Run tests","input_preview":"{\"command\":\"go test\"}"}}`)
	p.write(`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{}}`)
	m = p.next()
	if string(m.ID) != "7" || m.Error == nil || m.Error.Code != -32601 {
		t.Fatalf("unknown method answered %+v", m)
	}
	p.write(`{"jsonrpc":"2.0","id":8,"method":"ping"}`)
	if m = p.next(); string(m.ID) != "8" || m.Error != nil {
		t.Fatalf("ping answered %+v", m)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.reports) != 1 || f.reports[0].RequestID != "qwert" || f.reports[0].ToolName != "Bash" || f.reports[0].InputPreview != `{"command":"go test"}` {
		t.Fatalf("reports = %+v", f.reports)
	}
}

// End to end against the real service on a loopback listener: the owner
// types on the web, Claude Code gets a channel event; Claude Code relays a
// permission prompt, the owner answers on the web, Claude Code gets the verdict.
func TestChannelEndToEndThroughTheService(t *testing.T) {
	e, tok := termSessionEnv(t)
	channelPeer = channelPeerAllowed // a real socket has a real loopback peer
	stateDir := t.TempDir()
	key, err := WriteChannelKey(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	e.col.Channels = NewChannelHub(key, nil)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = e.app.Listener(ln, fiber.ListenConfig{DisableStartupMessage: true}) }()
	t.Cleanup(func() { _ = e.app.ShutdownWithTimeout(200 * time.Millisecond) })

	svc := &ChannelHTTP{
		BaseURL: "http://" + ln.Addr().String(),
		KeyPath: ChannelKeyPath(stateDir),
		Session: func() (string, error) { return chanTermSession, nil },
		Client:  &http.Client{Timeout: 40 * time.Second},
	}
	p := startChannel(t, svc)
	p.handshake()

	deadline := time.Now().Add(5 * time.Second)
	for !e.col.Channels.Live(chanTermSession) {
		if time.Now().After(deadline) {
			t.Fatal("the channel never registered")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if code, m, _ := e.do(t, "POST", "/aiagent/api/sessions/"+chanTermSession+"/message", tok, `{"message":"hello from the phone"}`); code != 200 {
		t.Fatalf("send: %d %v", code, m)
	}
	m := p.next()
	if m.Method != "notifications/claude/channel" || !strings.Contains(string(m.Params), "hello from the phone") {
		t.Fatalf("got %s %s", m.Method, m.Params)
	}

	p.write(`{"jsonrpc":"2.0","method":"notifications/claude/channel/permission_request","params":{"request_id":"zxcvb","tool_name":"Write","description":"Write main.go","input_preview":"{}"}}`)
	for !e.col.Channels.HasPrompt(chanTermSession) {
		if time.Now().After(deadline.Add(5 * time.Second)) {
			t.Fatal("the permission prompt never reached the service")
		}
		time.Sleep(20 * time.Millisecond)
	}
	time.Sleep(writeSpacing)
	if code, m, _ := e.do(t, "POST", "/aiagent/api/sessions/"+chanTermSession+"/answer", tok, `{"option":2}`); code != 200 {
		t.Fatalf("answer: %d %v", code, m)
	}
	m = p.next()
	if m.Method != "notifications/claude/channel/permission" || string(m.Params) != `{"behavior":"deny","request_id":"zxcvb"}` {
		t.Fatalf("verdict = %s %s", m.Method, m.Params)
	}
}

func TestChannelSessionIDComesFromTheParentsRegistryEntry(t *testing.T) {
	home := t.TempDir()
	none := func(string) string { return "" }
	writeFile(t, filepath.Join(home, ".claude", "sessions", "4242.json"), `{"pid":4242,"sessionId":"`+chanSessA+`","kind":"interactive"}`)
	if sid, err := ChannelSessionID(home, 4242, none); err != nil || sid != chanSessA {
		t.Fatalf("got %q %v", sid, err)
	}
	// an entry under the wrong pid is not ours
	writeFile(t, filepath.Join(home, ".claude", "sessions", "4343.json"), `{"pid":1,"sessionId":"`+chanSessA+`"}`)
	env := func(k string) string {
		if k == "CLAUDE_CODE_SESSION_ID" {
			return chanSessB
		}
		return ""
	}
	if sid, _ := ChannelSessionID(home, 4343, env); sid != chanSessB {
		t.Fatalf("fallback = %q, want the environment's", sid)
	}
	if _, err := ChannelSessionID(home, 4343, func(string) string { return "../x" }); err == nil {
		t.Fatal("a session id that is not one was accepted")
	}
}

func TestChannelNeverSendsTheKeyOffThisMachine(t *testing.T) {
	for _, ok := range []string{"http://127.0.0.1:8890", "http://localhost:8890", "http://[::1]:8890"} {
		if err := CheckChannelURL(ok); err != nil {
			t.Errorf("%s refused: %v", ok, err)
		}
	}
	for _, bad := range []string{"https://aiagent.joinanny.com", "http://192.168.1.5:8890", "http://0.0.0.0:8890", "file:///etc/passwd", "127.0.0.1:8890"} {
		if err := CheckChannelURL(bad); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}
}

func TestChannelKeyFileIsPrivate(t *testing.T) {
	dir := t.TempDir()
	key, err := WriteChannelKey(dir)
	if err != nil || len(key) != 64 {
		t.Fatalf("key %d %v", len(key), err)
	}
	fi, _ := os.Stat(ChannelKeyPath(dir))
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v, want 0600", fi.Mode().Perm())
	}
	if got, err := readChannelKey(ChannelKeyPath(dir)); err != nil || got != key {
		t.Fatalf("read back: %v", err)
	}
	_ = os.Chmod(ChannelKeyPath(dir), 0o644)
	if _, err := readChannelKey(ChannelKeyPath(dir)); err == nil {
		t.Fatal("a key other users can read was used")
	}
	RemoveChannelKey(dir)
	if _, err := readChannelKey(ChannelKeyPath(dir)); err == nil || !strings.Contains(err.Error(), "--channels") {
		t.Fatalf("missing key: %v", err)
	}
}
