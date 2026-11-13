// internal/aiagent/outbox.go
//
// Messages held for a session that is busy.
//
// The relay types into a CLI only while its prompt is on screen, so a message
// sent to an agent that is mid-task used to wait sixteen seconds and then fail
// with AIAGENT_RELAY_NOT_READY. On a phone that is the common case and the
// worst one: the operator attaches a screenshot, writes a line, taps send, and
// nothing arrives — the picture is already on the service but the text that
// referred to it never got typed.
//
// So a refused message is kept here instead and typed in as soon as the
// session is idle. The queue is on disk because the agent may be busy for
// longer than the browser stays open, and it only ever holds messages the
// operator already confirmed sending.
package world

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	outboxFile = "outbox.json"
	// A message typed into an agent hours after it was written reads as a
	// non sequitur, so the queue gives up rather than deliver something
	// stale. The operator sees it waiting and can cancel it before then.
	outboxTTL = 6 * time.Hour
	// Per session, so one unreachable agent cannot soak up the queue.
	outboxMax = 20
)

var outboxMu sync.Mutex

func pendingID(now time.Time) string {
	var rb [6]byte
	_, _ = rand.Read(rb[:])
	return "q" + now.UTC().Format("20060102-150405") + "-" + hex.EncodeToString(rb[:])
}

// Pending is one message waiting for its session to be free.
type Pending struct {
	ID        string    `json:"id"`
	SessionID string    `json:"sessionId"`
	Message   string    `json:"message"`
	QueuedAt  time.Time `json:"queuedAt"`
	Tries     int       `json:"tries"`
	LastError string    `json:"lastError,omitempty"`
}

func (s *Service) outboxPath() string { return filepath.Join(s.Store.dir, outboxFile) }

func (s *Service) loadOutbox() []Pending {
	out := []Pending{}
	b, err := os.ReadFile(s.outboxPath())
	if err != nil {
		return out
	}
	_ = json.Unmarshal(b, &out)
	return out
}

func (s *Service) saveOutbox(list []Pending) error {
	if err := os.MkdirAll(s.Store.dir, 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.outboxPath() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.outboxPath())
}

// fresh drops anything past the TTL. The caller holds outboxMu.
func fresh(list []Pending, now time.Time) []Pending {
	kept := list[:0]
	for _, p := range list {
		if now.Sub(p.QueuedAt) < outboxTTL {
			kept = append(kept, p)
		}
	}
	return kept
}

// Enqueue holds a message for a session that was not ready to receive it.
func (s *Service) Enqueue(sessionID, message string, now time.Time) (Pending, error) {
	outboxMu.Lock()
	defer outboxMu.Unlock()
	list := fresh(s.loadOutbox(), now)
	n := 0
	for _, p := range list {
		if p.SessionID == sessionID {
			n++
		}
	}
	if n >= outboxMax {
		return Pending{}, ErrConflict("too many messages already waiting for this agent")
	}
	p := Pending{ID: pendingID(now), SessionID: sessionID, Message: message, QueuedAt: now}
	list = append(list, p)
	return p, s.saveOutbox(list)
}

// PendingFor lists what is waiting, oldest first. An empty id means everything.
func (s *Service) PendingFor(sessionID string) []Pending {
	outboxMu.Lock()
	defer outboxMu.Unlock()
	out := []Pending{}
	for _, p := range s.loadOutbox() {
		if sessionID == "" || p.SessionID == sessionID {
			out = append(out, p)
		}
	}
	return out
}

// CancelPending drops one queued message, or every message for a session when
// the id is empty. It returns how many went.
func (s *Service) CancelPending(sessionID, id string) int {
	outboxMu.Lock()
	defer outboxMu.Unlock()
	list := s.loadOutbox()
	kept := list[:0]
	gone := 0
	for _, p := range list {
		if p.SessionID == sessionID && (id == "" || p.ID == id) {
			gone++
			continue
		}
		kept = append(kept, p)
	}
	if gone > 0 {
		_ = s.saveOutbox(kept)
	}
	return gone
}

// FlushOutbox tries every waiting message once. One that is still refused stays
// where it is; one that fails for any other reason is dropped, because retrying
// a message the service cannot deliver would type it in at some unrelated later
// moment. It returns how many were delivered.
func (s *Service) FlushOutbox(ctx context.Context, now time.Time) int {
	outboxMu.Lock()
	list := fresh(s.loadOutbox(), now)
	outboxMu.Unlock()
	if len(list) == 0 {
		return 0
	}
	sent := 0
	done := map[string]bool{}
	busy := map[string]bool{}
	for _, p := range list {
		// One refusal is enough: the rest for that session are refused too,
		// and each attempt costs the relay's full readiness wait.
		if busy[p.SessionID] {
			continue
		}
		_, err := s.Collector.SendToSession(ctx, p.SessionID, p.Message)
		if err == nil {
			done[p.ID] = true
			sent++
			continue
		}
		if ae, ok := err.(*APIError); ok && ae.Code == "AIAGENT_RELAY_NOT_READY" {
			busy[p.SessionID] = true
			continue
		}
		done[p.ID] = true
	}
	outboxMu.Lock()
	defer outboxMu.Unlock()
	cur := fresh(s.loadOutbox(), now)
	kept := cur[:0]
	for _, p := range cur {
		if done[p.ID] {
			continue
		}
		if busy[p.SessionID] {
			p.Tries++
		}
		kept = append(kept, p)
	}
	_ = s.saveOutbox(kept)
	return sent
}

// StartOutbox retries the queue while the service is switched on.
func (s *Service) StartOutbox(ctx context.Context) {
	go func() {
		t := time.NewTicker(20 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if s.Store.Enabled() {
					s.FlushOutbox(ctx, time.Now())
				}
			}
		}
	}()
}
