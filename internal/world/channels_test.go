package world

import (
	"context"
	"errors"
	"testing"
	"time"
)

const (
	chanSessA = "aaaa1111-0000-4000-8000-000000000001"
	chanSessB = "bbbb2222-0000-4000-8000-000000000002"
	chanKey   = "k3y-for-tests-only-0123456789abcdef"
)

func newTestHub(t *testing.T) (*ChannelHub, *time.Time) {
	t.Helper()
	now := time.Date(2026, 11, 28, 9, 0, 0, 0, time.UTC)
	return NewChannelHub(chanKey, func() time.Time { return now }), &now
}

func mustRegister(t *testing.T, h *ChannelHub, sid string) string {
	t.Helper()
	secret, err := h.Register(chanKey, sid)
	if err != nil || secret == "" {
		t.Fatalf("register %s: %q %v", sid, secret, err)
	}
	return secret
}

func isStatus(err error, status int) bool {
	var api *APIError
	return errors.As(err, &api) && api.Status == status
}

// A web message for a session reaches that session's channel, and only it.
func TestChannelDeliversAMessageOnlyToItsOwnSession(t *testing.T) {
	h, _ := newTestHub(t)
	a := mustRegister(t, h, chanSessA)
	b := mustRegister(t, h, chanSessB)
	if err := h.Send(chanSessA, "run the tests"); err != nil {
		t.Fatalf("send: %v", err)
	}
	got, err := h.Poll(context.Background(), chanSessA, a, 0)
	if err != nil || len(got) != 1 || got[0].Kind != "message" || got[0].Text != "run the tests" {
		t.Fatalf("A got %+v %v, want the one message", got, err)
	}
	if got, err := h.Poll(context.Background(), chanSessB, b, 0); err != nil || len(got) != 0 {
		t.Fatalf("B got %+v %v, want nothing", got, err)
	}
	if got, _ := h.Poll(context.Background(), chanSessA, a, 0); len(got) != 0 {
		t.Fatalf("a message was delivered twice: %+v", got)
	}
}

// The key and every session's secret are checked; one session's secret never
// opens another session's inbox.
func TestChannelRefusesAWrongKeyOrSecret(t *testing.T) {
	h, _ := newTestHub(t)
	if _, err := h.Register("wrong", chanSessA); !isStatus(err, 401) {
		t.Fatalf("register with a wrong key: %v, want 401", err)
	}
	if _, err := h.Register("", chanSessA); !isStatus(err, 401) {
		t.Fatalf("register with no key: %v, want 401", err)
	}
	if _, err := h.Register(chanKey, "../../etc"); !isStatus(err, 400) {
		t.Fatalf("register with a bad session id: %v, want 400", err)
	}
	a := mustRegister(t, h, chanSessA)
	mustRegister(t, h, chanSessB)
	_ = h.Send(chanSessB, "for B only")
	if _, err := h.Poll(context.Background(), chanSessB, a, 0); !isStatus(err, 401) {
		t.Fatalf("A's secret on B's inbox: %v, want 401", err)
	}
	if _, err := h.Poll(context.Background(), chanSessA, a+"x", 0); !isStatus(err, 401) {
		t.Fatalf("wrong secret: %v, want 401", err)
	}
	if err := h.Report(chanSessB, a, ChannelPermission{RequestID: "abcde", ToolName: "Bash"}); !isStatus(err, 401) {
		t.Fatalf("A's secret reporting B's question: %v, want 401", err)
	}
	if h.Prompt(chanSessB) != nil {
		t.Fatal("a refused report still put a question on B")
	}
}

// A second channel for the same session (a restart) replaces the first; the
// old secret stops working.
func TestChannelReRegisterRevokesTheOldSecret(t *testing.T) {
	h, _ := newTestHub(t)
	old := mustRegister(t, h, chanSessA)
	cur := mustRegister(t, h, chanSessA)
	if _, err := h.Poll(context.Background(), chanSessA, old, 0); !isStatus(err, 401) {
		t.Fatalf("old secret: %v, want 401", err)
	}
	if _, err := h.Poll(context.Background(), chanSessA, cur, 0); err != nil {
		t.Fatalf("new secret: %v", err)
	}
}

// A session is reachable only while its channel keeps polling.
func TestChannelIsLiveOnlyWhilePolled(t *testing.T) {
	h, now := newTestHub(t)
	if h.Live(chanSessA) {
		t.Fatal("live before any channel registered")
	}
	if err := h.Send(chanSessA, "hi"); !isStatus(err, 409) {
		t.Fatalf("send with no channel: %v, want 409", err)
	}
	a := mustRegister(t, h, chanSessA)
	if !h.Live(chanSessA) {
		t.Fatal("not live right after register")
	}
	*now = now.Add(2 * time.Minute)
	if h.Live(chanSessA) {
		t.Fatal("still live two minutes after the channel went quiet")
	}
	if _, err := h.Poll(context.Background(), chanSessA, a, 0); err != nil {
		t.Fatal(err)
	}
	if !h.Live(chanSessA) {
		t.Fatal("not live again after a poll")
	}
}

// A permission prompt the channel reports shows as a Yes/No question; the
// web's pick goes back to the channel as a verdict for that request id.
func TestChannelPermissionPromptRoundTrip(t *testing.T) {
	h, _ := newTestHub(t)
	a := mustRegister(t, h, chanSessA)
	if err := h.Report(chanSessA, a, ChannelPermission{RequestID: "Abcde", ToolName: "Bash"}); !isStatus(err, 400) {
		t.Fatalf("a request id outside Claude Code's alphabet: %v, want 400", err)
	}
	if err := h.Report(chanSessA, a, ChannelPermission{RequestID: "abcde", ToolName: "Bash", Description: "List files", InputPreview: `{"command":"ls"}`}); err != nil {
		t.Fatalf("report: %v", err)
	}
	p := h.Prompt(chanSessA)
	if p == nil || len(p.Options) != 2 || p.Options[0].N != 1 || p.Options[1].N != 2 {
		t.Fatalf("prompt = %+v, want a two-choice question", p)
	}
	if h.Prompt(chanSessB) != nil {
		t.Fatal("A's question shows on B")
	}
	if err := h.Answer(chanSessA, 3); !isStatus(err, 400) {
		t.Fatalf("option 3: %v, want 400", err)
	}
	if err := h.Answer(chanSessA, 2); err != nil {
		t.Fatalf("answer: %v", err)
	}
	if h.Prompt(chanSessA) != nil {
		t.Fatal("question still shown after it was answered")
	}
	got, _ := h.Poll(context.Background(), chanSessA, a, 0)
	if len(got) != 1 || got[0].Kind != "verdict" || got[0].RequestID != "abcde" || got[0].Behavior != "deny" {
		t.Fatalf("verdict = %+v, want deny abcde", got)
	}
	if err := h.Answer(chanSessA, 1); !isStatus(err, 409) {
		t.Fatalf("answering twice: %v, want 409", err)
	}
}

// A poll waits for a message rather than spinning.
func TestChannelPollWakesOnSend(t *testing.T) {
	h, _ := newTestHub(t)
	a := mustRegister(t, h, chanSessA)
	done := make(chan []ChannelEvent, 1)
	go func() {
		got, _ := h.Poll(context.Background(), chanSessA, a, 5*time.Second)
		done <- got
	}()
	time.Sleep(20 * time.Millisecond)
	_ = h.Send(chanSessA, "wake up")
	select {
	case got := <-done:
		if len(got) != 1 || got[0].Text != "wake up" {
			t.Fatalf("got %+v", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("poll did not wake on send")
	}
}
