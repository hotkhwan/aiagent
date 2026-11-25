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

	"github.com/hotkhwan/aiagent/internal/audit"
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

// flushMu keeps flushes one at a time, so a message found mid-send when a flush
// starts was left there by a process that died, not by a flush still running.
var flushMu sync.Mutex

// The life of a queued message. A message is only ever typed from
// PendingWaiting, and it is marked PendingSending on disk before the text
// reaches tmux, so a crash leaves evidence instead of a silent re-send.
const (
	PendingWaiting = ""        // queued, the agent was busy
	PendingSending = "sending" // being typed right now
	PendingUnknown = "unknown" // a send was interrupted; it may or may not have arrived
	PendingFailed  = "failed"  // the session refused it; kept so the operator sees why
)

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
	State     string    `json:"state,omitempty"`
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
	// fsync before the rename: the PendingSending mark is only worth anything
	// if it is on disk before the text is typed.
	tmp := s.outboxPath() + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
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
		return Pending{}, ErrConflict("มีข้อความรอส่งให้ agent นี้มากเกินไปแล้ว")
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

// FlushOutbox tries every waiting message once and returns how many were
// delivered.
//
// Each message is marked PendingSending on disk before it is typed and settled
// after, one at a time, so the file always says what may already be in a pane:
//   - delivered: removed;
//   - still refused as busy: back to waiting, tried again on the next tick;
//   - refused for any other reason: kept as PendingFailed with the reason and
//     never retried, because typing it at some unrelated later moment is worse
//     than not typing it at all;
//   - found still PendingSending when a flush starts: a previous process died
//     mid-send, so it becomes PendingUnknown and the queue never types it
//     again. The operator decides.
func (s *Service) FlushOutbox(ctx context.Context, now time.Time) int {
	flushMu.Lock()
	defer flushMu.Unlock()

	outboxMu.Lock()
	list := fresh(s.loadOutbox(), now)
	stranded := false
	var ids []string
	for i := range list {
		switch list[i].State {
		case PendingSending:
			list[i].State = PendingUnknown
			list[i].LastError = "บริการหยุดระหว่างพิมพ์ข้อความนี้ ข้อความอาจถึงแล้ว"
			stranded = true
		case PendingWaiting:
			ids = append(ids, list[i].ID)
		}
	}
	if stranded {
		_ = s.saveOutbox(list)
	}
	outboxMu.Unlock()

	sent := 0
	busy := map[string]bool{}
	for _, id := range ids {
		p, ok := s.claimPending(id, now, busy)
		if !ok {
			continue
		}
		s.auditOutbox("intent", p, nil)
		_, err := s.Collector.SendToSession(ctx, p.SessionID, p.Message)
		if isRelayBusy(err) {
			// One refusal is enough: the rest for that session are refused
			// too, and each attempt costs the relay's full readiness wait.
			busy[p.SessionID] = true
		} else if err == nil {
			sent++
		}
		s.settlePending(id, err)
		s.auditOutbox("result", p, err)
	}
	return sent
}

func isRelayBusy(err error) bool {
	ae, ok := err.(*APIError)
	return ok && ae.Code == "AIAGENT_RELAY_NOT_READY"
}

// claimPending marks one waiting message as being sent, on disk, and returns
// it. It re-reads the queue under the lock, so a message cancelled since the
// flush began is not typed.
func (s *Service) claimPending(id string, now time.Time, busy map[string]bool) (Pending, bool) {
	outboxMu.Lock()
	defer outboxMu.Unlock()
	list := fresh(s.loadOutbox(), now)
	for i := range list {
		if list[i].ID != id {
			continue
		}
		if list[i].State != PendingWaiting || busy[list[i].SessionID] {
			return Pending{}, false
		}
		list[i].State = PendingSending
		if err := s.saveOutbox(list); err != nil {
			// Without the mark on disk a crash could re-send it; skip this
			// tick rather than type without a record.
			return Pending{}, false
		}
		return list[i], true
	}
	return Pending{}, false
}

// settlePending records how a send ended.
func (s *Service) settlePending(id string, err error) {
	outboxMu.Lock()
	defer outboxMu.Unlock()
	list := s.loadOutbox()
	kept := list[:0]
	for _, p := range list {
		if p.ID == id {
			if err == nil {
				continue
			}
			p.Tries++
			if isRelayBusy(err) {
				p.State = PendingWaiting
			} else {
				p.State = PendingFailed
				p.LastError = trimErr(err.Error())
			}
		}
		kept = append(kept, p)
	}
	_ = s.saveOutbox(kept)
}

// auditOutbox records a queued send the way an HTTP write is recorded: the
// intent before the text is typed, the result after.
func (s *Service) auditOutbox(event string, p Pending, err error) {
	if s.Auditor == nil {
		return
	}
	r := audit.Record{Event: event, RPC: "aiagent.outbox", AdminSub: "outbox", ParamsHash: paramsHash(p.SessionID, p.ID)}
	if event == "result" {
		r.Result = "ok"
		if err != nil {
			r.Result = "error"
			r.Err = trimErr(err.Error())
		}
	}
	_ = s.Auditor.Write(r)
}

// trimErr shortens an error for the queue file, the API and the audit log, and
// runs it through Redact: it carries CLI output, and the queue is served to
// the browser.
func trimErr(s string) string {
	s = Redact(s)
	const max = 200
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
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
					s.Collector.reapRelays(ctx, time.Now())
				}
			}
		}
	}()
}
