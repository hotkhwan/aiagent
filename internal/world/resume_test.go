package world

import (
	"path/filepath"
	"strings"
	"testing"
)

func resumeCalls(e *env) []string {
	var got []string
	for _, c := range e.runner.callsTo("tmux") {
		if c[0] == "new-session" {
			got = append(got, strings.Join(c, " "))
		}
	}
	return got
}

// After a reboot nothing runs but the transcript is still there. Resume starts
// the CLI on that conversation in its own folder and types nothing into it.
func TestResumeStartsAFinishedSessionWithoutTyping(t *testing.T) {
	e := newEnv(t)
	tok, _ := e.store.Enable("test")
	writeFile(t, filepath.Join(e.home, ".claude", "projects", "-root", outboxSession+".jsonl"),
		`{"type":"user","timestamp":"2026-11-04T10:00:00Z","message":{"role":"user","content":"ทำต่อ"}}`+"\n")

	code, m, _ := e.do(t, "POST", "/aiagent/api/sessions/"+outboxSession+"/resume", tok, "")
	if code != 200 {
		t.Fatalf("resume: %d %v", code, m)
	}
	if state := m["details"].(map[string]any)["state"]; state != "started" {
		t.Fatalf("state = %v, want started", state)
	}
	got := resumeCalls(e)
	want := "new-session -d -s aiagent-aaaa1111 -x 220 -y 50 -c /root claude --resume " + outboxSession
	if len(got) != 1 || got[0] != want {
		t.Fatalf("new-session calls = %q, want [%q]", got, want)
	}
	for _, c := range e.runner.callsTo("tmux") {
		if c[0] == "send-keys" {
			t.Fatalf("resume must not type into the session: %v", c)
		}
	}
}

// A session that is already running is not started a second time.
func TestResumeLeavesARunningSessionAlone(t *testing.T) {
	e := outboxEnv(t)
	tok, _ := e.store.Enable("test")

	code, m, _ := e.do(t, "POST", "/aiagent/api/sessions/"+outboxSession+"/resume", tok, "")
	if code != 200 {
		t.Fatalf("resume: %d %v", code, m)
	}
	if state := m["details"].(map[string]any)["state"]; state != "running" {
		t.Fatalf("state = %v, want running", state)
	}
	if got := resumeCalls(e); len(got) != 0 {
		t.Fatalf("a running session must not be started again: %q", got)
	}
}

// When the CLI exits at once, the operator is told why instead of seeing "started".
func TestResumeReportsTheCLIsRefusal(t *testing.T) {
	e := newEnv(t)
	tok, _ := e.store.Enable("test")
	writeFile(t, filepath.Join(e.home, ".claude", "projects", "-root", outboxSession+".jsonl"),
		`{"type":"user","timestamp":"2026-11-04T10:00:00Z","message":{"role":"user","content":"ทำต่อ"}}`+"\n")
	e.runner.paneDead = true

	code, m, _ := e.do(t, "POST", "/aiagent/api/sessions/"+outboxSession+"/resume", tok, "")
	if code != 502 {
		t.Fatalf("resume against a dead pane: %d %v, want 502", code, m)
	}
}
