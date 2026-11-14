package taskstore

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

// An attempt holds its task — and its project — while it is starting, running
// or unknown. The other statuses are endings.
const (
	AttemptStarting AttemptStatus = "starting" // dispatch recorded, not yet confirmed received
	AttemptRunning  AttemptStatus = "running"  // the agent has the work
	AttemptUnknown  AttemptStatus = "unknown"  // the service stopped before confirming; reconcile, do not resend

	AttemptReported  AttemptStatus = "reported"  // the worker says it is done; verification decides
	AttemptFailed    AttemptStatus = "failed"    // it went wrong
	AttemptCancelled AttemptStatus = "cancelled" // stopped on purpose
	AttemptOrphaned  AttemptStatus = "orphaned"  // its worker is gone
)

func (a AttemptStatus) live() bool {
	return a == AttemptStarting || a == AttemptRunning || a == AttemptUnknown
}

// AttemptStart is what a new attempt is dispatched with.
type AttemptStart struct {
	Provider   string `json:"provider,omitempty"`
	Workspace  string `json:"workspace,omitempty"`
	BaseCommit string `json:"baseCommit,omitempty"`
	Branch     string `json:"branch,omitempty"`
}

type attemptStartData struct {
	AttemptStart
	LeaseSeconds int64 `json:"leaseSeconds"`
}

type attemptEndData struct {
	Status AttemptStatus `json:"status"`
	Reason string        `json:"reason,omitempty"`
}

type attemptRunningData struct {
	SessionID string `json:"sessionId,omitempty"`
}

const evAttemptUnknown = "attemptUnknown"

// liveAttempt is the attempt still holding the task, if any.
func (t *Task) liveAttempt() *Attempt {
	for i := range t.Attempts {
		if t.Attempts[i].Status.live() {
			return &t.Attempts[i]
		}
	}
	return nil
}

// StartAttempt dispatches a ready task: it records the attempt as starting
// and the task as running, under a lease. A project has one live attempt at a
// time.
func (s *Store) StartAttempt(taskID string, st AttemptStart, lease time.Duration, idemKey string) (Task, Attempt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var id string
	_, err := s.appendEvents(func() ([]Event, error) {
		if e, ok := s.byKey[idemKey]; idemKey != "" && ok {
			id = e.AttemptID
			return nil, nil
		}
		t, ok := s.tasks[taskID]
		if !ok {
			return nil, fmt.Errorf("no task %s", taskID)
		}
		id = fmt.Sprintf("%s-A%d", taskID, len(t.Attempts)+1)
		data, _ := json.Marshal(attemptStartData{AttemptStart: st, LeaseSeconds: int64(lease / time.Second)})
		return []Event{{Actor: ActorOrchestrator, Type: evAttemptStarting, TaskID: taskID, AttemptID: id, IdempotencyKey: idemKey, Data: data}}, nil
	})
	if err != nil {
		return Task{}, Attempt{}, err
	}
	t := s.copyOf(taskID)
	a, _ := s.findAttempt(id)
	return t, a, nil
}

// MarkRunning records that the agent received the dispatch.
func (s *Store) MarkRunning(attemptID, sessionID string) (Attempt, error) {
	data, _ := json.Marshal(attemptRunningData{SessionID: sessionID})
	return s.attemptEvent(attemptID, evAttemptRunning, ActorOrchestrator, data, "")
}

// MarkUnknown records that a dispatch was typed but its receipt could not be
// confirmed. The attempt keeps holding its task; nothing resends it.
func (s *Store) MarkUnknown(attemptID string) (Attempt, error) {
	return s.attemptEvent(attemptID, evAttemptUnknown, ActorOrchestrator, nil, "")
}

// Heartbeat renews an attempt's lease from now.
func (s *Store) Heartbeat(attemptID string) (Attempt, error) {
	return s.attemptEvent(attemptID, evAttemptBeat, ActorOrchestrator, nil, "")
}

// EndAttempt ends an attempt. It releases the task, which the orchestrator
// then moves on by the state machine.
func (s *Store) EndAttempt(attemptID string, status AttemptStatus, reason, idemKey string) (Attempt, error) {
	return s.endAttemptAs(attemptID, status, reason, idemKey, ActorOrchestrator)
}

func (s *Store) endAttemptAs(attemptID string, status AttemptStatus, reason, idemKey string, actor Actor) (Attempt, error) {
	data, _ := json.Marshal(attemptEndData{Status: status, Reason: reason})
	return s.attemptEvent(attemptID, evAttemptEnded, actor, data, idemKey)
}

func (s *Store) attemptEvent(attemptID, typ string, actor Actor, data json.RawMessage, idemKey string) (Attempt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.appendEvents(func() ([]Event, error) {
		if _, ok := s.byKey[idemKey]; idemKey != "" && ok {
			return nil, nil
		}
		taskID, ok := s.attempt[attemptID]
		if !ok {
			return nil, fmt.Errorf("no attempt %s", attemptID)
		}
		return []Event{{Actor: actor, Type: typ, TaskID: taskID, AttemptID: attemptID, IdempotencyKey: idemKey, Data: data}}, nil
	})
	if err != nil {
		return Attempt{}, err
	}
	a, _ := s.findAttempt(attemptID)
	return a, nil
}

// Expired lists live attempts whose lease ran out before now. It changes
// nothing: the worker may still be typing, and has to be stopped before its
// task goes to anyone else.
func (s *Store) Expired(now time.Time) []Attempt {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refresh()
	var out []Attempt
	for _, t := range s.tasks {
		if a := t.liveAttempt(); a != nil && a.LeaseExpiresAt.Before(now) {
			out = append(out, *a)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Recover runs when the service starts. An attempt still starting was being
// dispatched when the service stopped, and nobody knows whether the agent got
// it: it becomes unknown, keeps holding its task, and is never dispatched
// again by itself. It returns how many attempts it marked.
func (s *Store) Recover() (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	evs, err := s.appendEvents(func() ([]Event, error) {
		var out []Event
		for _, t := range s.tasks {
			for _, a := range t.Attempts {
				if a.Status == AttemptStarting {
					out = append(out, Event{Actor: ActorOrchestrator, Type: evAttemptUnknown, TaskID: t.ID, AttemptID: a.ID})
				}
			}
		}
		sort.Slice(out, func(i, j int) bool { return out[i].AttemptID < out[j].AttemptID })
		return out, nil
	})
	return len(evs), err
}

func (s *Store) findAttempt(id string) (Attempt, bool) {
	t, ok := s.tasks[s.attempt[id]]
	if !ok {
		return Attempt{}, false
	}
	for _, a := range t.Attempts {
		if a.ID == id {
			return a, true
		}
	}
	return Attempt{}, false
}

func (s *Store) applyAttempt(t *Task, e Event) error {
	if e.Actor != ActorOrchestrator {
		return fmt.Errorf("%s may not change attempts; workers report through the orchestrator", e.Actor)
	}
	if e.Type == evAttemptStarting {
		return s.applyStart(t, e)
	}
	var a *Attempt
	for i := range t.Attempts {
		if t.Attempts[i].ID == e.AttemptID {
			a = &t.Attempts[i]
		}
	}
	if a == nil {
		return fmt.Errorf("no attempt %s on task %s", e.AttemptID, t.ID)
	}
	if !a.Status.live() {
		return fmt.Errorf("attempt %s already ended (%s)", a.ID, a.Status)
	}
	switch e.Type {
	case evAttemptRunning:
		var d attemptRunningData
		if err := json.Unmarshal(e.Data, &d); err != nil {
			return err
		}
		a.Status, a.SessionID = AttemptRunning, d.SessionID
		a.renew(e.At)
	case evAttemptBeat:
		a.renew(e.At)
	case evAttemptUnknown:
		if a.Status != AttemptStarting {
			return fmt.Errorf("attempt %s is %s, not starting", a.ID, a.Status)
		}
		a.Status = AttemptUnknown
	case evAttemptEnded:
		var d attemptEndData
		if err := json.Unmarshal(e.Data, &d); err != nil {
			return err
		}
		if d.Status.live() || d.Status == "" {
			return fmt.Errorf("%q is not how an attempt ends", d.Status)
		}
		at := e.At
		a.Status, a.ExitReason, a.EndedAt = d.Status, d.Reason, &at
	default:
		return fmt.Errorf("unknown event type %q", e.Type)
	}
	return nil
}

func (s *Store) applyStart(t *Task, e Event) error {
	if t.Status != StatusReady {
		return fmt.Errorf("task %s is %s; only a ready task can start an attempt", t.ID, t.Status)
	}
	for _, o := range s.tasks {
		if o.ProjectID == t.ProjectID {
			if a := o.liveAttempt(); a != nil {
				return fmt.Errorf("project %s already has attempt %s running; one writer per project", t.ProjectID, a.ID)
			}
		}
	}
	var d attemptStartData
	if err := json.Unmarshal(e.Data, &d); err != nil {
		return err
	}
	a := Attempt{
		ID: e.AttemptID, TaskRevision: t.Revision, Status: AttemptStarting,
		Provider: d.Provider, Workspace: d.Workspace, BaseCommit: d.BaseCommit, Branch: d.Branch,
		StartedAt: e.At, LeaseSeconds: d.LeaseSeconds,
	}
	a.renew(e.At)
	t.Attempts = append(t.Attempts, a)
	t.Status = StatusRunning
	s.attempt[a.ID] = t.ID
	return nil
}

// renew moves the heartbeat and the lease to at.
func (a *Attempt) renew(at time.Time) {
	a.HeartbeatAt = at
	a.LeaseExpiresAt = at.Add(time.Duration(a.LeaseSeconds) * time.Second)
}
