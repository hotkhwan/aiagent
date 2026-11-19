package taskstore

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// AttemptStatus is where one attempt is.
type AttemptStatus string

// Change is what a transition carries besides its target: why, and what proves
// it is allowed.
type Change struct {
	Reason   string            `json:"reason,omitempty"`
	Evidence map[string]string `json:"evidence,omitempty"`
}

// rule allows one move. The table is the whole state machine: a move that is
// not in it is refused, whoever asks.
type rule struct {
	from, to Status
	actors   []Actor
	evidence []string // keys Change.Evidence must carry
	reason   bool     // Change.Reason is required
}

var (
	orch = []Actor{ActorOrchestrator}
	user = []Actor{ActorUser}
	both = []Actor{ActorOrchestrator, ActorUser}
)

// Transitions is the task state machine. ready → running is not here: only
// StartAttempt moves a task to running, because running means a worker holds
// it. Workers and the PM appear nowhere — they report and propose, and the
// orchestrator or the user decides.
var Transitions = []rule{
	{from: StatusQueued, to: StatusReady, actors: orch},
	{from: StatusReady, to: StatusQueued, actors: both},
	{from: StatusQueued, to: StatusBlocked, actors: both, reason: true},
	{from: StatusReady, to: StatusBlocked, actors: both, reason: true},
	{from: StatusBlocked, to: StatusQueued, actors: both},

	{from: StatusRunning, to: StatusVerifying, actors: orch, evidence: []string{"commit"}},
	{from: StatusRunning, to: StatusQueued, actors: orch, reason: true},
	{from: StatusRunning, to: StatusFailed, actors: orch, reason: true},

	{from: StatusVerifying, to: StatusAwaitingReview, actors: orch, evidence: []string{"tests"}},
	{from: StatusVerifying, to: StatusQueued, actors: orch, reason: true},
	{from: StatusVerifying, to: StatusFailed, actors: orch, reason: true},

	// Only the user completes; a revision request starts the task over at
	// the next revision.
	{from: StatusAwaitingReview, to: StatusVerifying, actors: orch, reason: true},
	{from: StatusAwaitingReview, to: StatusCompleted, actors: user},
	{from: StatusAwaitingReview, to: StatusQueued, actors: user, reason: true},

	{from: StatusFailed, to: StatusQueued, actors: user},

	{from: StatusQueued, to: StatusCancelled, actors: user},
	{from: StatusReady, to: StatusCancelled, actors: user},
	{from: StatusBlocked, to: StatusCancelled, actors: user},
	{from: StatusRunning, to: StatusCancelled, actors: user},
	{from: StatusVerifying, to: StatusCancelled, actors: user},
	{from: StatusAwaitingReview, to: StatusCancelled, actors: user},
	{from: StatusFailed, to: StatusCancelled, actors: user},
}

func findRule(from, to Status) (rule, bool) {
	for _, r := range Transitions {
		if r.from == from && r.to == to {
			return r, true
		}
	}
	return rule{}, false
}

type transitionData struct {
	From Status `json:"from"`
	To   Status `json:"to"`
	Change
}

// Transition moves a task by the state machine. A repeated idempotency key
// returns the task as it is without writing anything.
func (s *Store) Transition(taskID string, to Status, actor Actor, ch Change, idemKey string) (Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ch.Reason = strings.TrimSpace(ch.Reason)
	_, err := s.appendEvents(func() ([]Event, error) {
		if _, ok := s.byKey[idemKey]; idemKey != "" && ok {
			return nil, nil
		}
		t, ok := s.tasks[taskID]
		if !ok {
			return nil, fmt.Errorf("no task %s", taskID)
		}
		data, _ := json.Marshal(transitionData{From: t.Status, To: to, Change: ch})
		return []Event{{Actor: actor, Type: evTaskTransition, TaskID: taskID, IdempotencyKey: idemKey, Data: data}}, nil
	})
	if err != nil {
		return Task{}, err
	}
	return s.copyOf(taskID), nil
}

// checkTransition is the state machine's judgement on one move.
func checkTransition(t *Task, d transitionData, actor Actor) error {
	if d.From != t.Status {
		return fmt.Errorf("task %s is %s, not %s", t.ID, t.Status, d.From)
	}
	r, ok := findRule(d.From, d.To)
	if !ok {
		return fmt.Errorf("task %s cannot go from %s to %s", t.ID, d.From, d.To)
	}
	if !slices.Contains(r.actors, actor) {
		return fmt.Errorf("%s may not move a task from %s to %s", actor, d.From, d.To)
	}
	if r.reason && d.Reason == "" {
		return fmt.Errorf("moving a task from %s to %s needs a reason", d.From, d.To)
	}
	for _, k := range r.evidence {
		if strings.TrimSpace(d.Evidence[k]) == "" {
			return fmt.Errorf("moving a task from %s to %s needs evidence %q", d.From, d.To, k)
		}
	}
	if d.From == StatusRunning && d.To != StatusCancelled {
		if a := t.liveAttempt(); a != nil {
			return fmt.Errorf("attempt %s still holds task %s: end it before moving the task on", a.ID, t.ID)
		}
	}
	return nil
}

func (s *Store) applyLifecycle(e Event) error {
	t, ok := s.tasks[e.TaskID]
	if !ok {
		return fmt.Errorf("event %s for unknown task %s", e.Type, e.TaskID)
	}
	switch e.Type {
	case evTaskTransition:
		var d transitionData
		if err := json.Unmarshal(e.Data, &d); err != nil {
			return err
		}
		if err := checkTransition(t, d, e.Actor); err != nil {
			return err
		}
		if d.From == StatusAwaitingReview && d.To == StatusQueued {
			t.Revision++
		}
		t.Status = d.To
		t.BlockedReason = ""
		if d.To == StatusBlocked {
			t.BlockedReason = d.Reason
		}
	default:
		if err := s.applyAttempt(t, e); err != nil {
			return err
		}
	}
	t.UpdatedAt = e.At
	return nil
}
