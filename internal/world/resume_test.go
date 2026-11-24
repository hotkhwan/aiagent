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
	// In the background, under the daemon: an interactive CLI in a tmux session
	// of ours would hold the session and lock the operator's own clients out.
	wake := "claude --bg --resume " + outboxSession
	if runs := claudeRuns(e); len(runs) != 1 || "claude "+runs[0] != wake || e.runner.runDirs[wake] != "/root" {
		t.Fatalf("claude runs = %q in %q, want [%q] in /root", runs, e.runner.runDirs[wake], wake)
	}
	if got := resumeCalls(e); len(got) != 0 {
		t.Fatalf("resume must not open a tmux session: %q", got)
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
	if got, runs := resumeCalls(e), claudeRuns(e); len(got) != 0 || len(runs) != 0 {
		t.Fatalf("a running session must not be started again: %q %q", got, runs)
	}
}

// When the CLI can only offer a copy, the operator is told why instead of
// seeing "started" over a session that is not the one they picked.
func TestResumeReportsASessionOpenElsewhere(t *testing.T) {
	e := newEnv(t)
	tok, _ := e.store.Enable("test")
	writeFile(t, filepath.Join(e.home, ".claude", "projects", "-root", outboxSession+".jsonl"),
		`{"type":"user","timestamp":"2026-11-04T10:00:00Z","message":{"role":"user","content":"ทำต่อ"}}`+"\n")
	e.runner.bgResumeOut = "backgrounded · cccc9999 (idle — send a prompt to start)\n"

	code, m, _ := e.do(t, "POST", "/aiagent/api/sessions/"+outboxSession+"/resume", tok, "")
	if code != 409 {
		t.Fatalf("resume of a session open elsewhere: %d %v, want 409", code, m)
	}
	if runs := strings.Join(claudeRuns(e), "\n"); !strings.Contains(runs, "stop cccc9999") {
		t.Fatalf("the copy was left running; claude runs:\n%s", runs)
	}
}
