// Package taskstore keeps the PM's tasks and their attempts as an append-only
// event log: one JSON object per line, appended under an exclusive file lock
// and fsynced before a write returns.
//
// The log is the source of truth and the current state is rebuilt from it
// whenever the store is opened, so there is never a table that disagrees with
// the history. It needs no database and no dependency beyond the standard
// library — the module keeps to one external dependency — and a single-user
// pilot holds at most a few thousand events. If that stops being true, the
// Store API is the seam to put SQLite behind.
//
// JSON keys are camelCase, like the rest of the API.
package taskstore

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Actor is who caused an event.
type Actor string

const (
	ActorUser         Actor = "user"         // the operator, through the API or CLI
	ActorOrchestrator Actor = "orchestrator" // the service's own deterministic control
	ActorPM           Actor = "pm"           // the planning model: proposes, never decides
	ActorWorker       Actor = "worker"       // an agent doing the work: reports, never decides
)

// NewTask is what a task is created from.
type NewTask struct {
	ProjectID          string   `json:"projectId"`
	Title              string   `json:"title"`
	Kind               string   `json:"kind,omitempty"` // bug | feature | ux | regression | security | chore
	Priority           int      `json:"priority,omitempty"`
	Provider           string   `json:"provider,omitempty"` // claude | codex | gemini | "" for the PM to choose
	AcceptanceCriteria []string `json:"acceptanceCriteria"`
	AllowedPaths       []string `json:"allowedPaths,omitempty"`
	BlockedBy          []string `json:"blockedBy,omitempty"`
	RiskLabels         []string `json:"riskLabels,omitempty"`
	MaxAttempts        int      `json:"maxAttempts,omitempty"`
}

// Task is a task's current state, folded from its events.
type Task struct {
	ID string `json:"taskId"`
	NewTask
	Revision      int       `json:"taskRevision"`
	Status        Status    `json:"status"`
	BlockedReason string    `json:"blockedReason,omitempty"`
	CreatedBy     Actor     `json:"createdBy"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
	Attempts      []Attempt `json:"attempts"`
	deleted       bool
}

// Event is one line of the log.
type Event struct {
	Seq            int64           `json:"seq"`
	At             time.Time       `json:"at"`
	Actor          Actor           `json:"actor"`
	Type           string          `json:"type"`
	TaskID         string          `json:"taskId,omitempty"`
	AttemptID      string          `json:"attemptId,omitempty"`
	IdempotencyKey string          `json:"idempotencyKey,omitempty"`
	Data           json.RawMessage `json:"data,omitempty"`
}

// ErrCorrupt means a line other than the last one is unreadable: the log was
// damaged by something other than a crash mid-append, and guessing past it
// could rebuild the wrong state.
var ErrCorrupt = errors.New("task log is corrupt")

// Store is the task log and the state folded from it.
type Store struct {
	path string
	Now  func() time.Time

	mu      sync.Mutex
	size    int64 // bytes of the log already applied
	seq     int64
	nextID  int
	tasks   map[string]*Task
	byKey   map[string]Event // idempotency key → the event it produced
	events  []Event
	attempt map[string]string // attemptId → taskId
}

// Open loads the log at path, creating it if it does not exist.
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	s := &Store{path: path, Now: time.Now}
	s.reset()
	err := s.locked(func(f *os.File) error { return s.catchUp(f, true) })
	if err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) reset() {
	s.size, s.seq, s.nextID = 0, 0, 1
	s.tasks = map[string]*Task{}
	s.byKey = map[string]Event{}
	s.attempt = map[string]string{}
	s.events = nil
}

// locked runs fn with the log open and exclusively locked, so a second
// process (the CLI next to the service) cannot interleave an append.
func (s *Store) locked(fn func(*os.File) error) error {
	f, err := os.OpenFile(s.path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return fn(f)
}

// catchUp applies whatever another process appended since the last read. On
// open it also cuts off a torn last line — half an append left by a crash —
// so the next write starts on a clean line.
func (s *Store) catchUp(f *os.File, opening bool) error {
	fi, err := f.Stat()
	if err != nil {
		return err
	}
	if fi.Size() < s.size {
		// Shorter than what was applied: rebuild from scratch.
		s.reset()
	}
	if fi.Size() == s.size {
		return nil
	}
	if _, err := f.Seek(s.size, io.SeekStart); err != nil {
		return err
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return err
	}
	complete := data
	tail := []byte(nil)
	if i := bytes.LastIndexByte(data, '\n'); i < len(data)-1 {
		complete, tail = data[:i+1], data[i+1:]
	}
	sc := bufio.NewScanner(bytes.NewReader(complete))
	sc.Buffer(make([]byte, 64<<10), 16<<20)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var e Event
		if err := json.Unmarshal(line, &e); err != nil {
			return fmt.Errorf("%w: %s", ErrCorrupt, s.path)
		}
		if err := s.apply(e); err != nil {
			return fmt.Errorf("%w: event %d: %v", ErrCorrupt, e.Seq, err)
		}
	}
	if err := sc.Err(); err != nil {
		return err
	}
	s.size += int64(len(complete))
	if len(tail) > 0 {
		if !opening {
			return fmt.Errorf("%w: unterminated line written by another process", ErrCorrupt)
		}
		if err := f.Truncate(s.size); err != nil {
			return err
		}
	}
	return nil
}

// appendEvents writes events as whole lines and applies them. They are
// numbered and stamped here, under the lock, after catching up.
func (s *Store) appendEvents(build func() ([]Event, error)) ([]Event, error) {
	var out []Event
	err := s.locked(func(f *os.File) error {
		if err := s.catchUp(f, false); err != nil {
			return err
		}
		evs, err := build()
		if err != nil || len(evs) == 0 {
			return err
		}
		var buf bytes.Buffer
		now := s.Now().UTC()
		for i := range evs {
			evs[i].Seq = s.seq + int64(i) + 1
			evs[i].At = now
			b, err := json.Marshal(evs[i])
			if err != nil {
				return err
			}
			// Keep what the log holds rather than what the caller passed: a
			// caller's string may sit in a buffer that is reused after the call.
			var own Event
			if err := json.Unmarshal(b, &own); err != nil {
				return err
			}
			evs[i] = own
			buf.Write(b)
			buf.WriteByte('\n')
		}
		// Check every event against the state before anything is written,
		// so a refused change leaves no trace in the log.
		probe := s.clone()
		for _, e := range evs {
			if err := probe.apply(e); err != nil {
				return err
			}
		}
		if _, err := f.Seek(0, io.SeekEnd); err != nil {
			return err
		}
		if _, err := f.Write(buf.Bytes()); err != nil {
			return err
		}
		if err := f.Sync(); err != nil {
			return err
		}
		for _, e := range evs {
			_ = s.apply(e)
		}
		s.size += int64(buf.Len())
		out = evs
		return nil
	})
	return out, err
}

// clone copies the folded state deeply enough for a dry-run apply.
func (s *Store) clone() *Store {
	c := &Store{seq: s.seq, nextID: s.nextID, tasks: map[string]*Task{}, byKey: map[string]Event{}, attempt: map[string]string{}}
	for id, t := range s.tasks {
		cp := *t
		cp.Attempts = append([]Attempt(nil), t.Attempts...)
		c.tasks[id] = &cp
	}
	for k, v := range s.byKey {
		c.byKey[k] = v
	}
	for k, v := range s.attempt {
		c.attempt[k] = v
	}
	return c
}

// Create adds a task in the queued state. A repeated idempotency key returns
// the task the first call created instead of creating another.
func (s *Store) Create(nt NewTask, actor Actor, idemKey string) (Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	nt.ProjectID = strings.TrimSpace(nt.ProjectID)
	nt.Title = strings.TrimSpace(nt.Title)
	switch {
	case nt.ProjectID == "":
		return Task{}, errors.New("a task needs a projectId")
	case nt.Title == "":
		return Task{}, errors.New("a task needs a title")
	case len(nonEmpty(nt.AcceptanceCriteria)) == 0:
		return Task{}, errors.New("a task needs at least one acceptance criterion: without one it cannot be verified")
	case actor != ActorUser && actor != ActorOrchestrator:
		return Task{}, fmt.Errorf("%s cannot create tasks; the PM proposes and the user confirms", actor)
	}
	nt.AcceptanceCriteria = nonEmpty(nt.AcceptanceCriteria)
	if nt.MaxAttempts <= 0 {
		nt.MaxAttempts = 2
	}
	var id string
	_, err := s.appendEvents(func() ([]Event, error) {
		if e, ok := s.byKey[idemKey]; idemKey != "" && ok {
			id = e.TaskID
			return nil, nil
		}
		id = fmt.Sprintf("T-%04d", s.nextID)
		data, _ := json.Marshal(nt)
		return []Event{{Actor: actor, Type: evTaskCreated, TaskID: id, IdempotencyKey: idemKey, Data: data}}, nil
	})
	if err != nil {
		return Task{}, err
	}
	return s.copyOf(id), nil
}

// Get returns one task.
func (s *Store) Get(id string) (Task, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refresh()
	if t, ok := s.tasks[id]; !ok || t.deleted {
		return Task{}, false
	}
	return s.copyOf(id), true
}

// List returns every task that has not been deleted, oldest first.
func (s *Store) List() []Task {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refresh()
	out := make([]Task, 0, len(s.tasks))
	for id, t := range s.tasks {
		if !t.deleted {
			out = append(out, s.copyOf(id))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Events returns a task's history in order.
func (s *Store) Events(taskID string) []Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refresh()
	var out []Event
	for _, e := range s.events {
		if e.TaskID == taskID {
			out = append(out, e)
		}
	}
	return out
}

// refresh picks up another process's appends before a read.
func (s *Store) refresh() {
	_ = s.locked(func(f *os.File) error { return s.catchUp(f, false) })
}

func (s *Store) copyOf(id string) Task {
	t := *s.tasks[id]
	t.Attempts = append([]Attempt{}, t.Attempts...)
	return t
}

func nonEmpty(xs []string) []string {
	var out []string
	for _, x := range xs {
		if x = strings.TrimSpace(x); x != "" {
			out = append(out, x)
		}
	}
	return out
}
