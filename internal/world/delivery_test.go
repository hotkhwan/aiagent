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

// sendWithTranscript sends text while the transcript gains line after it is
// typed, and returns what the send reports.
func sendWithTranscript(t *testing.T, text, line string) any {
	t.Helper()
	e := outboxEnv(t)
	writeFile(t, outboxTranscript(e), `{"type":"assistant","timestamp":"2026-11-04T10:00:00Z","message":{"role":"assistant","content":"พร้อม"}}`+"\n")
	e.runner.afterType = func() {
		f, err := os.OpenFile(outboxTranscript(e), os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			t.Error(err)
			return
		}
		defer f.Close()
		_, _ = f.WriteString(line + "\n")
	}
	return sendAndReadDelivery(t, e, text)
}

// A long paste is recorded inside a pasted_content block, and a message taken
// in mid-task as an attachment; both reached the agent.
func TestDeliveryIsSeenInEveryFormClaudeCodeRecords(t *testing.T) {
	pasted := `\n\n<pasted_content id=\"ca43\">\nบรรทัดหนึ่ง\nบรรทัดสอง\n</pasted_content id=\"ca43\">\n`
	msg := `บรรทัดหนึ่ง\nบรรทัดสอง`
	for name, c := range map[string]struct{ line, want string }{
		"pasted block":     {`{"type":"user","timestamp":"2026-11-04T10:20:01Z","message":{"role":"user","content":"` + pasted + `"}}`, "acked"},
		"absorbed in turn": {`{"type":"attachment","timestamp":"2026-11-04T10:20:01Z","attachment":{"type":"queued_command","commandMode":"prompt","prompt":"` + pasted + `"}}`, "acked"},
		"in the CLI queue": {`{"type":"queue-operation","operation":"enqueue","timestamp":"2026-11-04T10:20:01Z","content":"` + pasted + `"}`, "held"},
		"a notification":   {`{"type":"attachment","timestamp":"2026-11-04T10:20:01Z","attachment":{"type":"queued_command","commandMode":"task-notification","prompt":"` + msg + `"}}`, "unknown"},
		"an earlier turn":  {`{"type":"queue-operation","operation":"enqueue","timestamp":"2026-11-04T10:00:01Z","content":"` + msg + `"}`, "unknown"},
	} {
		t.Run(name, func(t *testing.T) {
			if got := sendWithTranscript(t, msg, c.line); got != c.want {
				t.Fatalf("delivery = %v, want %s", got, c.want)
			}
		})
	}
	if got := sendWithTranscript(t, "/compact", `{}`); got != "command" {
		t.Fatalf("a slash command leaves no user turn: delivery = %v", got)
	}
}

// The paste tags are Claude Code's, not the person's: the chat shows the text.
func TestChatShowsPastedAndAbsorbedMessages(t *testing.T) {
	got := claudeMessages([][]byte{
		[]byte(`{"type":"user","timestamp":"2026-11-04T10:20:01Z","message":{"role":"user","content":"\n\n<pasted_content id=\"ca43\">\nก\nข\n</pasted_content id=\"ca43\">\n"}}`),
		[]byte(`{"type":"attachment","timestamp":"2026-11-04T10:20:02Z","attachment":{"type":"queued_command","commandMode":"prompt","prompt":"ค"}}`),
	})
	if len(got) != 2 || got[0].Text != "ก\nข" || got[1].Text != "ค" || got[1].Role != "user" {
		t.Fatalf("messages = %+v", got)
	}
}

// A message to a workspace is checked against its agent's transcript too.
func TestAWorkspaceMessageReportsDelivery(t *testing.T) {
	e := newEnv(t)
	e.runner.sessions, e.runner.panes = "api\t0\n", "api\tagent\t1\tclaude\t1\n"
	tok, _ := e.store.Enable("test")
	code, m, _ := e.do(t, "POST", "/aiagent/api/messages", tok, `{"workspace":"api","message":"สวัสดี"}`)
	if code != 200 {
		t.Fatalf("%d %v", code, m)
	}
	d, _ := m["details"].(map[string]any)
	if d["delivery"] != "unknown" {
		t.Fatalf("delivery = %v, want unknown: nothing was seen in a transcript", d["delivery"])
	}
}
