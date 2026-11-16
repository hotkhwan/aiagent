package world

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Two senders typing into one session at once — the API, the outbox, the
// model policy, a review reply — must not interleave: text A, text B, Enter,
// Enter submits "AB" as one garbled instruction. Each send owns the session
// from its text to its Enter.
func TestSendsToOneSessionDoNotInterleave(t *testing.T) {
	e := outboxEnv(t)

	// The first sender pauses after typing, long enough for an unguarded
	// second sender to type into the same prompt.
	var typed int32
	second := make(chan struct{})
	e.runner.afterType = func() {
		if atomic.AddInt32(&typed, 1) == 1 {
			select {
			case <-second:
			case <-time.After(300 * time.Millisecond):
			}
			return
		}
		close(second)
	}

	var wg sync.WaitGroup
	for _, text := range []string{"ข้อความ A", "ข้อความ B"} {
		wg.Add(1)
		go func(text string) {
			defer wg.Done()
			if _, err := e.col.SendToSession(context.Background(), outboxSession, text); err != nil {
				t.Errorf("send %q: %v", text, err)
			}
		}(text)
		time.Sleep(20 * time.Millisecond) // A starts first
	}
	wg.Wait()

	var keys []string
	for _, c := range e.runner.callsTo("tmux") {
		if c[0] == "send-keys" {
			keys = append(keys, c[len(c)-1])
		}
	}
	want := []string{"ข้อความ A", "Enter", "ข้อความ B", "Enter"}
	if len(keys) != 4 || keys[0] != want[0] || keys[1] != want[1] || keys[2] != want[2] || keys[3] != want[3] {
		t.Fatalf("send-keys order = %q, want %q", keys, want)
	}
}
