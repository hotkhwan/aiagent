package world

import (
	"encoding/json"
	"io"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const chanTermSession = "bbbb2222-0000-0000-0000-000000000000"

// channelCall is one request from a channel process.
func channelCall(t *testing.T, e *env, path, key, secret, body string, hdr map[string]string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest("POST", path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set(ChannelKeyHeader, key)
	}
	if secret != "" {
		req.Header.Set(ChannelSecretHeader, secret)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := e.app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	return resp.StatusCode, m
}

func channelRegister(t *testing.T, e *env, sid string) string {
	t.Helper()
	code, m := channelCall(t, e, "/aiagent/channel/register", chanKey, "", `{"sessionId":"`+sid+`"}`, nil)
	if code != 200 {
		t.Fatalf("register: %d %v", code, m)
	}
	return m["details"].(map[string]any)["secret"].(string)
}

func channelPoll(t *testing.T, e *env, sid, secret string) []any {
	t.Helper()
	code, m := channelCall(t, e, "/aiagent/channel/poll?wait=0", "", secret, `{"sessionId":"`+sid+`"}`, nil)
	if code != 200 {
		t.Fatalf("poll: %d %v", code, m)
	}
	ev, _ := m["details"].(map[string]any)["events"].([]any)
	return ev
}

// an interactive terminal / VS Code session, live, in the service's home
func termSessionEnv(t *testing.T) (*env, string) {
	e := newEnv(t)
	tok, _ := e.store.Enable("test")
	fakeProcPPID(t, e.proc, "5002", "52", "1")
	writeFile(t, filepath.Join(e.home, ".claude", "sessions", "5002.json"), `{"pid":5002,"sessionId":"`+chanTermSession+`","cwd":"/root","procStart":"52","kind":"interactive","status":"idle"}`)
	// fiber's in-memory connection has peer 0.0.0.0; stand in loopback,
	// keeping the header checks
	channelPeer = func(_, cf, xff, fwd string) bool { return channelPeerAllowed("127.0.0.1", cf, xff, fwd) }
	t.Cleanup(func() { channelPeer = channelPeerAllowed })
	return e, tok
}

func TestChannelPeerIsOnlyADirectLoopbackCaller(t *testing.T) {
	cases := []struct {
		peer, cf, xff, fwd string
		want               bool
	}{
		{"127.0.0.1", "", "", "", true},
		{"::1", "", "", "", true},
		{"0.0.0.0", "", "", "", false},
		{"192.168.1.20", "", "", "", false},
		{"127.0.0.1", "203.0.113.9", "", "", false},
		{"127.0.0.1", "", "203.0.113.9", "", false},
		{"127.0.0.1", "", "", "for=203.0.113.9", false},
		{"", "", "", "", false},
	}
	for _, tc := range cases {
		if got := channelPeerAllowed(tc.peer, tc.cf, tc.xff, tc.fwd); got != tc.want {
			t.Errorf("channelPeerAllowed(%q, %q, %q, %q) = %v, want %v", tc.peer, tc.cf, tc.xff, tc.fwd, got, tc.want)
		}
	}
}

// With channels off (the default) nothing of them is reachable, and an
// interactive session stays read-only exactly as before.
func TestChannelRoutesAreAbsentByDefault(t *testing.T) {
	e, tok := termSessionEnv(t)
	if code, _ := channelCall(t, e, "/aiagent/channel/register", chanKey, "", `{"sessionId":"`+chanTermSession+`"}`, nil); code != 404 {
		t.Fatalf("register with channels off: %d, want 404", code)
	}
	if code, _, _ := e.do(t, "POST", "/aiagent/api/sessions/"+chanTermSession+"/message", tok, `{"message":"hi"}`); code != 409 {
		t.Fatalf("interactive session without a channel must stay read-only: %d", code)
	}
}

func TestChannelRoutesRefuseWrongCredentialsAndTheTunnel(t *testing.T) {
	e, _ := termSessionEnv(t)
	e.col.Channels = NewChannelHub(chanKey, nil)
	body := `{"sessionId":"` + chanTermSession + `"}`
	if code, _ := channelCall(t, e, "/aiagent/channel/register", "nope", "", body, nil); code != 401 {
		t.Fatalf("wrong key: %d, want 401", code)
	}
	// through the Cloudflare tunnel every request is loopback; the tunnel's
	// header gives it away, and a channel is never remote
	if code, _ := channelCall(t, e, "/aiagent/channel/register", chanKey, "", body, map[string]string{"Cf-Connecting-Ip": "203.0.113.9"}); code != 404 {
		t.Fatalf("via the tunnel: %d, want 404", code)
	}
	if code, _ := channelCall(t, e, "/aiagent/channel/register", chanKey, "", body, map[string]string{"X-Forwarded-For": "203.0.113.9"}); code != 404 {
		t.Fatalf("via a proxy: %d, want 404", code)
	}
	secret := channelRegister(t, e, chanTermSession)
	if code, _ := channelCall(t, e, "/aiagent/channel/poll?wait=0", "", secret+"0", body, nil); code != 401 {
		t.Fatalf("wrong secret: %d, want 401", code)
	}
	other := `{"sessionId":"cccc3333-0000-0000-0000-000000000000"}`
	if code, _ := channelCall(t, e, "/aiagent/channel/poll?wait=0", "", secret, other, nil); code != 401 {
		t.Fatalf("own secret on another session: %d, want 401", code)
	}
	if code, _ := channelCall(t, e, "/aiagent/channel/poll?wait=0", chanKey, "", body, nil); code != 401 {
		t.Fatalf("the key is not a session secret: %d, want 401", code)
	}
}

// The owner types in the web; the message goes to the session's channel, not
// into any terminal.
func TestWebMessageToAnIDESessionGoesThroughItsChannel(t *testing.T) {
	e, tok := termSessionEnv(t)
	e.col.Channels = NewChannelHub(chanKey, nil)
	secret := channelRegister(t, e, chanTermSession)

	code, m, _ := e.do(t, "POST", "/aiagent/api/sessions/"+chanTermSession+"/message", tok, `{"message":"run go test"}`)
	if code != 200 {
		t.Fatalf("send: %d %v", code, m)
	}
	ev := channelPoll(t, e, chanTermSession, secret)
	if len(ev) != 1 || ev[0].(map[string]any)["text"] != "run go test" {
		t.Fatalf("channel got %v", ev)
	}
	for _, c := range e.runner.callsTo("tmux") {
		if c[0] == "send-keys" || c[0] == "new-session" {
			t.Fatalf("something was typed into a terminal: %q", c)
		}
	}
	_, snap, _ := e.do(t, "GET", "/aiagent/api/snapshot", tok, "")
	found := false
	for _, s := range snap["details"].(map[string]any)["sessions"].([]any) {
		s := s.(map[string]any)
		if s["sessionId"] == chanTermSession {
			found = true
			if s["messageVia"] != "channel" {
				t.Fatalf("messageVia = %v, want channel", s["messageVia"])
			}
		}
	}
	if !found {
		t.Fatal("the session is not in the snapshot")
	}
}

// A permission prompt in the IDE shows on the web, and the web's answer goes
// back to the channel as a verdict.
func TestIDEPermissionPromptAnsweredFromTheWeb(t *testing.T) {
	e, tok := termSessionEnv(t)
	e.col.Channels = NewChannelHub(chanKey, nil)
	secret := channelRegister(t, e, chanTermSession)
	code, m := channelCall(t, e, "/aiagent/channel/permission", "", secret,
		`{"sessionId":"`+chanTermSession+`","requestId":"qwert","toolName":"Bash","description":"Run the tests","inputPreview":"{\"command\":\"go test ./...\"}"}`, nil)
	if code != 200 {
		t.Fatalf("report: %d %v", code, m)
	}
	_, s, _ := e.do(t, "GET", "/aiagent/api/sessions/"+chanTermSession, tok, "")
	b, _ := json.Marshal(s)
	if !strings.Contains(string(b), "Run the tests") {
		t.Fatalf("the question is not on the session: %s", b)
	}
	time.Sleep(writeSpacing)
	if code, m, _ := e.do(t, "POST", "/aiagent/api/sessions/"+chanTermSession+"/answer", tok, `{"option":1}`); code != 200 {
		t.Fatalf("answer: %d %v", code, m)
	}
	ev := channelPoll(t, e, chanTermSession, secret)
	if len(ev) != 1 {
		t.Fatalf("events %v", ev)
	}
	v := ev[0].(map[string]any)
	if v["kind"] != "verdict" || v["requestId"] != "qwert" || v["behavior"] != "allow" {
		t.Fatalf("verdict = %v", v)
	}
}
