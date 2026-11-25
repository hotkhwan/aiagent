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
// transcript on disk and no process behind it. `claude attach` wakes such a
// job under the daemon and draws its prompt (measured on Claude Code 2.1.325).
func doneJobEnv(t *testing.T) *env {
	t.Helper()
	e := newEnv(t)
	e.now = time.Now()
	e.runner.agentsJSON = `[{"id":"bbbb2222","sessionId":"` + doneJob + `","name":"littleDays PM","kind":"background","state":"done"}]`
	writeFile(t, filepath.Join(e.home, ".claude", "projects", "-root", doneJob+".jsonl"),
		`{"type":"user","timestamp":"2026-11-04T03:00:00Z","message":{"content":"go"}}`+"\n")
	e.runner.trustOnAttach = true
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

// claudeRuns lists the `claude` commands run outside tmux, argv joined.
func claudeRuns(e *env) []string {
	var got []string
	for _, c := range e.runner.callsTo("claude") {
		if c[0] != "agents" {
			got = append(got, strings.Join(c, " "))
		}
	}
	return got
}

func relayKilled(e *env) bool {
	for _, c := range e.runner.callsTo("tmux") {
		if c[0] == "kill-session" {
			return true
		}
	}
	return false
}

// The relay must never start an interactive `claude --resume` of its own: that
// process owns the session, so the operator's own agent view answers "Can't
// open — this session is running in another terminal", and opening it anyway
// starts a copy under a new id (owner, 2026-11-24: "session ที่ผมใช้บน PC ขึ้นว่า
// open in terminal ทำให้ผมเข้าไม่ได้"). A job the daemon lists is joined with
// attach, finished or not, so every client shares the one session.
func TestSendToADoneJobAttachesToTheSharedSession(t *testing.T) {
	e := doneJobEnv(t)
	if _, err := e.col.SendToSession(context.Background(), doneJob, "ตรวจแล้ว ผ่าน"); err != nil {
		t.Fatalf("send to a done job: %v", err)
	}
	starts := relayStarts(e)
	if len(starts) != 1 || !strings.HasSuffix(starts[0], " claude attach bbbb2222") {
		t.Fatalf("relay starts = %q, want one claude attach bbbb2222", starts)
	}
	if runs := claudeRuns(e); len(runs) != 0 {
		t.Fatalf("a job the daemon lists needs no other command: %q", runs)
	}
	if timesTyped(e, "ตรวจแล้ว ผ่าน") != 1 {
		t.Fatalf("the answer was not typed into the attached session")
	}
}

// A session the daemon no longer lists is brought back as a background job
// under its own id, in the folder its transcript is filed under (the agent's
// last cwd, a worktree say, would become the job's folder), and then attached.
func TestAnUnlistedSessionIsWokenInTheBackgroundThenAttached(t *testing.T) {
	e := outboxEnv(t)
	e.runner.noJob = true
	writeFile(t, filepath.Join(e.home, ".claude", "projects", "-root", outboxSession+".jsonl"),
		`{"type":"user","cwd":"/root/repo/.claude/worktrees/x","timestamp":"2026-11-04T03:00:00Z","message":{"content":"go"}}`+"\n")
	if _, err := e.col.SendToSession(context.Background(), outboxSession, "ทำต่อ"); err != nil {
		t.Fatalf("send: %v", err)
	}
	wake := "claude --bg --resume " + outboxSession
	if runs := claudeRuns(e); len(runs) != 1 || "claude "+runs[0] != wake {
		t.Fatalf("claude runs = %q, want [%q]", runs, wake)
	}
	if dir := e.runner.runDirs[wake]; dir != "/root" {
		t.Fatalf("woken in %q, want /root", dir)
	}
	for _, s := range relayStarts(e) {
		if !strings.HasSuffix(s, " claude attach aaaa1111") {
			t.Fatalf("relay starts = %q, want attach only", relayStarts(e))
		}
	}
	if timesTyped(e, "ทำต่อ") != 1 {
		t.Fatalf("the message was not typed once into the woken session")
	}
}

// When another process has the session open the CLI starts a COPY under a new
// id and says so. Typing there sends the work to a different CLI than the one
// the operator picked (owner, 2026-11-24: "บางทีสั่งหน้า web มาส่ง cli คนละตัว").
func TestACopyOfTheSessionIsStoppedAndNeverTypedInto(t *testing.T) {
	e := outboxEnv(t)
	e.runner.noJob = true
	e.runner.bgResumeOut = "backgrounded · cccc9999 (idle — send a prompt to start)\n"
	_, err := e.col.SendToSession(context.Background(), outboxSession, "ทำต่อ")
	if err == nil || !strings.Contains(err.Error(), "Claude Code อีกที่") {
		t.Fatalf("err = %v, want one that says the session is open elsewhere", err)
	}
	if timesTyped(e, "ทำต่อ") != 0 {
		t.Fatal("the message was typed although the CLI only offered a copy")
	}
	runs := strings.Join(claudeRuns(e), "\n")
	if !strings.Contains(runs, "stop cccc9999") || !strings.Contains(runs, "rm cccc9999") {
		t.Fatalf("the copy was left behind; claude runs:\n%s", runs)
	}
}

// An attach the CLI refuses for any other reason is reported as it is; waking
// the session then would only make the copy.
func TestAnAttachRefusedBecauseTheSessionIsOpenElsewhereIsReported(t *testing.T) {
	e := doneJobEnv(t)
	e.runner.deadScreen = "Couldn't wake bbbb2222 — Can't open — this session is running in another terminal"
	_, err := e.col.SendToSession(context.Background(), doneJob, "ตรวจแล้ว ผ่าน")
	if err == nil || !strings.Contains(err.Error(), "running in another terminal") {
		t.Fatalf("err = %v, want the CLI's own reason", err)
	}
	if runs := claudeRuns(e); len(runs) != 0 {
		t.Fatalf("nothing may be started for a session open elsewhere: %q", runs)
	}
}

// Relays made before this fix run `claude --resume` and hold the session. An
// idle one is replaced by an attach, which frees the session for every client.
func TestAnIdleResumeRelayIsReplacedByAnAttach(t *testing.T) {
	e := doneJobEnv(t)
	e.runner.relayUp = true
	e.runner.paneStart = "new-session -d -s aiagent-bbbb2222 -x 220 -y 50 -c /root claude --resume " + doneJob
	if _, err := e.col.SendToSession(context.Background(), doneJob, "ตรวจแล้ว ผ่าน"); err != nil {
		t.Fatalf("send past an old resume relay: %v", err)
	}
	starts := relayStarts(e)
	if !relayKilled(e) || len(starts) != 1 || !strings.HasSuffix(starts[0], " claude attach bbbb2222") {
		t.Fatalf("killed %v, relay starts %q: want the resume relay replaced by an attach", relayKilled(e), starts)
	}
	if timesTyped(e, "ตรวจแล้ว ผ่าน") != 1 {
		t.Fatal("the answer was not typed once")
	}
}

// One that is mid-task is left alone: killing it would lose the work.
func TestABusyResumeRelayIsLeftRunning(t *testing.T) {
	e := doneJobEnv(t)
	e.runner.relayUp = true
	e.runner.relayBusy = true
	e.runner.paneStart = "new-session -d -s aiagent-bbbb2222 -x 220 -y 50 -c /root claude --resume " + doneJob
	_, err := e.col.SendToSession(context.Background(), doneJob, "ตรวจแล้ว ผ่าน")
	if ae, ok := err.(*APIError); !ok || ae.Code != "AIAGENT_RELAY_NOT_READY" {
		t.Fatalf("err = %v, want AIAGENT_RELAY_NOT_READY", err)
	}
	if relayKilled(e) || timesTyped(e, "ตรวจแล้ว ผ่าน") != 0 {
		t.Fatal("a working CLI must be neither killed nor typed into")
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
