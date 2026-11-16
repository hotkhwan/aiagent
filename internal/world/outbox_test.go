package world

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const outboxSession = "aaaa1111-0000-0000-0000-000000000000"

// outboxEnv is a service with one idle background session the relay can type
// into, and nothing queued yet.
func outboxEnv(t *testing.T) *env {
	t.Helper()
	e := newEnv(t)
	e.store.Enable("test")
	fakeProcPPID(t, e.proc, "5001", "51", "1")
	writeFile(t, filepath.Join(e.home, ".claude", "sessions", "5001.json"),
		`{"pid":5001,"sessionId":"`+outboxSession+`","cwd":"/root","procStart":"51","kind":"bg","status":"idle"}`)
	return e
}

// timesTyped counts how often text was typed into a pane.
func timesTyped(e *env, text string) int {
	n := 0
	for _, c := range e.runner.callsTo("tmux") {
		if c[0] == "send-keys" && c[len(c)-1] == text {
			n++
		}
	}
	return n
}

func flushSurvivingPanic(e *env) (crashed bool) {
	defer func() { crashed = recover() != nil }()
	e.svc.FlushOutbox(context.Background(), e.now)
	return false
}

// A crash between typing a queued message and recording that it went must not
// type it a second time: the text may already be in the agent's prompt, and a
// duplicate instruction is worse than one the operator has to check.
func TestOutboxDoesNotRetypeAMessageAfterACrashMidSend(t *testing.T) {
	e := outboxEnv(t)
	if _, err := e.svc.Enqueue(outboxSession, "แก้ outbox", e.now); err != nil {
		t.Fatal(err)
	}

	e.runner.crashOnEnter = true
	if !flushSurvivingPanic(e) {
		t.Fatal("the simulated crash did not happen")
	}
	e.runner.crashOnEnter = false

	if sent := e.svc.FlushOutbox(context.Background(), e.now); sent != 0 {
		t.Fatalf("after a crash mid-send nothing may be resent; sent %d", sent)
	}
	if n := timesTyped(e, "แก้ outbox"); n != 1 {
		t.Fatalf("the message was typed %d times, want exactly once", n)
	}
	got := e.svc.PendingFor(outboxSession)
	if len(got) != 1 || got[0].State != PendingUnknown {
		t.Fatalf("the operator must see the message as delivery-unknown; got %+v", got)
	}
}

// A message the session refuses for a reason other than being busy used to
// vanish without a word. It stays now, marked failed with the reason, and the
// queue does not try it again.
func TestOutboxKeepsARefusedMessageWithItsReasonAndDoesNotRetry(t *testing.T) {
	e := outboxEnv(t)
	if _, err := e.svc.Enqueue(outboxSession, "ข้อความ", e.now); err != nil {
		t.Fatal(err)
	}
	e.runner.paneDead = true
	e.svc.FlushOutbox(context.Background(), e.now)
	e.svc.FlushOutbox(context.Background(), e.now)

	got := e.svc.PendingFor(outboxSession)
	if len(got) != 1 || got[0].State != PendingFailed {
		t.Fatalf("a refused message must stay visible as failed; got %+v", got)
	}
	if !strings.Contains(got[0].LastError, "No job matching") {
		t.Errorf("the reason should say what the CLI said; got %q", got[0].LastError)
	}
	if got[0].Tries != 1 {
		t.Errorf("a failed message must not be retried; tries = %d", got[0].Tries)
	}
}

// A queued send reaches an agent as surely as one typed through the API, so it
// leaves the same audit trail: the intent before, the result after.
func TestOutboxAuditsIntentBeforeResult(t *testing.T) {
	e := outboxEnv(t)
	if _, err := e.svc.Enqueue(outboxSession, "ข้อความ", e.now); err != nil {
		t.Fatal(err)
	}
	if sent := e.svc.FlushOutbox(context.Background(), e.now); sent != 1 {
		t.Fatalf("sent %d, want 1", sent)
	}
	b, err := os.ReadFile(e.auditFP)
	if err != nil {
		t.Fatal(err)
	}
	var events []string
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if strings.Contains(line, `"aiagent.outbox"`) {
			var r struct {
				Event  string `json:"event"`
				Result string `json:"result"`
			}
			_ = json.Unmarshal([]byte(line), &r)
			events = append(events, r.Event+":"+r.Result)
		}
	}
	if strings.Join(events, ",") != "intent:,result:ok" {
		t.Fatalf("want intent then result:ok for the outbox send; got %v", events)
	}
}
