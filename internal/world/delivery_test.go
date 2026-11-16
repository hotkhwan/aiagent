package world

import (
	"os"
	"path/filepath"
	"testing"
)

func outboxTranscript(e *env) string {
	return filepath.Join(e.home, ".claude", "projects", "-root", outboxSession+".jsonl")
}

// sendAndReadDelivery posts a message to the outbox session and returns the
// delivery field of the answer.
func sendAndReadDelivery(t *testing.T, e *env, text string) any {
	t.Helper()
	tok, _ := e.store.Enable("test")
	code, m, _ := e.do(t, "POST", "/aiagent/api/sessions/"+outboxSession+"/message", tok, `{"message":"`+text+`"}`)
	if code != 200 {
		t.Fatalf("send: %d %v", code, m)
	}
	d, _ := m["details"].(map[string]any)
	return d["delivery"]
}

// tmux exiting 0 only says keys were pressed. A message counts as delivered
// when the agent's own transcript shows it as a user turn after the send.
func TestASendIsAckedWhenTheTranscriptShowsIt(t *testing.T) {
	e := outboxEnv(t)
	writeFile(t, outboxTranscript(e), `{"type":"assistant","timestamp":"2026-11-04T10:00:00Z","message":{"role":"assistant","content":"พร้อม"}}`+"\n")
	e.runner.afterType = func() {
		f, err := os.OpenFile(outboxTranscript(e), os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			t.Error(err)
			return
		}
		defer f.Close()
		_, _ = f.WriteString(`{"type":"user","timestamp":"2026-11-04T10:20:01Z","message":{"role":"user","content":"แก้ outbox ให้เสร็จ"}}` + "\n")
	}

	if got := sendAndReadDelivery(t, e, "แก้ outbox ให้เสร็จ"); got != "acked" {
		t.Fatalf("delivery = %v, want acked", got)
	}
}

// Waiting for the transcript must not rescan the whole machine on every poll.
// It did: each of the twenty polls rebuilt the snapshot — every session file,
// tmux, the daemon — which pushed an unacked send past fiber's one-second test
// timeout on CI's slower macOS runner, and cost the same in production. The
// session's transcript is found once and then only that file is read.
func TestWaitingForDeliveryDoesNotRescanTheMachine(t *testing.T) {
	e := outboxEnv(t)
	writeFile(t, outboxTranscript(e), `{"type":"assistant","timestamp":"2026-11-04T10:00:00Z","message":{"role":"assistant","content":"พร้อม"}}`+"\n")
	before := len(e.runner.callsTo("tmux"))
	scans := func() int {
		n := 0
		for _, c := range e.runner.callsTo("tmux")[before:] {
			if c[0] == "list-sessions" {
				n++
			}
		}
		return n
	}

	if got := sendAndReadDelivery(t, e, "แก้ outbox ให้เสร็จ"); got != "unknown" {
		t.Fatalf("delivery = %v, want unknown", got)
	}
	if n := scans(); n > 4 {
		t.Fatalf("an unacked send rescanned the machine %d times; it should find the transcript once", n)
	}
}

// No user turn in the transcript means nobody can say whether the agent got
// it: the answer is unknown, never a success it cannot prove.
func TestASendWithNoTranscriptEvidenceIsUnknown(t *testing.T) {
	e := outboxEnv(t)
	writeFile(t, outboxTranscript(e), `{"type":"assistant","timestamp":"2026-11-04T10:00:00Z","message":{"role":"assistant","content":"พร้อม"}}`+"\n")

	if got := sendAndReadDelivery(t, e, "แก้ outbox ให้เสร็จ"); got != "unknown" {
		t.Fatalf("delivery = %v, want unknown", got)
	}
}
