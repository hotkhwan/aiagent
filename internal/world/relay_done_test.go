package world

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const doneJob = "bbbb2222-0000-0000-0000-000000000000"

// doneJobEnv is a background job the daemon still lists, finished, with its
// transcript on disk and no process behind it.
func doneJobEnv(t *testing.T) *env {
	t.Helper()
	e := newEnv(t)
	e.now = time.Now()
	e.runner.agentsJSON = `[{"id":"bbbb2222","sessionId":"` + doneJob + `","name":"littleDays PM","kind":"background","state":"done"}]`
	writeFile(t, filepath.Join(e.home, ".claude", "projects", "-root", doneJob+".jsonl"),
		`{"type":"user","timestamp":"2026-11-04T03:00:00Z","message":{"content":"go"}}`+"\n")
	e.runner.attachHangs = true
	return e
}

func relayStarts(e *env) []string {
	var got []string
	for _, c := range e.runner.callsTo("tmux") {
		if c[0] == "new-session" {
			got = append(got, strings.Join(c, " "))
		}
	}
	return got
}

// A review answer to a finished job was reported as "probably still working"
// for ever: attach to a done job never draws a prompt, and resume was never tried.
func TestSendToADoneJobResumesIt(t *testing.T) {
	e := doneJobEnv(t)
	if _, err := e.col.SendToSession(context.Background(), doneJob, "ตรวจแล้ว ผ่าน"); err != nil {
		t.Fatalf("send to a done job: %v", err)
	}
	starts := relayStarts(e)
	if len(starts) != 1 || !strings.Contains(starts[0], "--resume "+doneJob) {
		t.Fatalf("relay starts = %q, want one claude --resume %s", starts, doneJob)
	}
	if timesTyped(e, "ตรวจแล้ว ผ่าน") != 1 {
		t.Fatalf("the answer was not typed into the resumed session")
	}
}

// --resume only finds a conversation from the folder its transcript is filed
// under. The agent's last cwd (a worktree, say) makes the CLI exit at once.
func TestResumeRunsWhereTheTranscriptIsFiled(t *testing.T) {
	e := doneJobEnv(t)
	writeFile(t, filepath.Join(e.home, ".claude", "projects", "-root", doneJob+".jsonl"),
		`{"type":"user","cwd":"/root/repo/.claude/worktrees/x","timestamp":"2026-11-04T03:00:00Z","message":{"content":"go"}}`+"\n")
	if _, err := e.col.SendToSession(context.Background(), doneJob, "ตรวจแล้ว ผ่าน"); err != nil {
		t.Fatalf("send: %v", err)
	}
	if starts := relayStarts(e); len(starts) != 1 || !strings.Contains(starts[0], " -c /root claude --resume ") {
		t.Fatalf("relay starts = %q, want --resume run in /root", starts)
	}
}

// The trust dialog's ❯ is not a prompt: typing the answer and Enter there
// picks "No, exit". Say so instead of typing.
func TestTrustDialogIsNotAPrompt(t *testing.T) {
	e := doneJobEnv(t)
	e.runner.trustPrompt = true
	_, err := e.col.SendToSession(context.Background(), doneJob, "ตรวจแล้ว ผ่าน")
	if err == nil || !strings.Contains(err.Error(), "trust") {
		t.Fatalf("err = %v, want one that says the folder must be trusted", err)
	}
	for _, c := range e.runner.callsTo("tmux") {
		if c[0] == "send-keys" {
			t.Fatalf("nothing may be typed into the trust dialog: %v", c)
		}
	}
}

// The hung attach relay left behind by an earlier try must not keep blocking.
func TestSendReplacesAHungAttachRelay(t *testing.T) {
	e := doneJobEnv(t)
	e.runner.relayUp = true
	e.runner.paneStart = "new-session -d -s aiagent-bbbb2222 -x 220 -y 50 claude attach bbbb2222"
	if _, err := e.col.SendToSession(context.Background(), doneJob, "ตรวจแล้ว ผ่าน"); err != nil {
		t.Fatalf("send past a hung attach relay: %v", err)
	}
	killed := false
	for _, c := range e.runner.callsTo("tmux") {
		if c[0] == "kill-session" {
			killed = true
		}
	}
	if starts := relayStarts(e); !killed || len(starts) != 1 || !strings.Contains(starts[0], "--resume "+doneJob) {
		t.Fatalf("killed %v, relay starts %q: want the hung attach replaced by a resume", killed, starts)
	}
}
