package taskstore

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Status is where a task is in its life.
type Status string

const (
	StatusQueued         Status = "queued"
	StatusReady          Status = "ready"
	StatusRunning        Status = "running"
	StatusVerifying      Status = "verifying"
	StatusAwaitingReview Status = "awaitingReview"
	StatusCompleted      Status = "completed"
	StatusBlocked        Status = "blocked"
	StatusFailed         Status = "failed"
	StatusCancelled      Status = "cancelled"
)

// Attempt is one try at a task by one worker.
type Attempt struct {
	ID             string        `json:"attemptId"`
	TaskRevision   int           `json:"taskRevision"`
	Status         AttemptStatus `json:"status"`
	Provider       string        `json:"provider,omitempty"`
	Workspace      string        `json:"workspace,omitempty"`
	SessionID      string        `json:"sessionId,omitempty"`
	BaseCommit     string        `json:"baseCommit,omitempty"`
	Branch         string        `json:"branch,omitempty"`
	StartedAt      time.Time     `json:"startedAt"`
	HeartbeatAt    time.Time     `json:"heartbeatAt"`
	LeaseExpiresAt time.Time     `json:"leaseExpiresAt"`
	EndedAt        *time.Time    `json:"endedAt,omitempty"`
	ExitReason     string        `json:"exitReason,omitempty"`
}

const (
	evTaskCreated     = "taskCreated"
	evTaskTransition  = "taskTransitioned"
	evAttemptStarting = "attemptStarting"
	evAttemptRunning  = "attemptRunning"
	evAttemptBeat     = "attemptHeartbeat"
	evAttemptEnded    = "attemptEnded"
)

// apply folds one event into the state. It is the only code that changes a
// task, so replaying the log always rebuilds the same state.
func (s *Store) apply(e Event) error {
	if e.Seq != s.seq+1 {
		return fmt.Errorf("sequence %d after %d", e.Seq, s.seq)
	}
	switch e.Type {
	case evTaskCreated:
		if _, dup := s.tasks[e.TaskID]; dup {
			return fmt.Errorf("task %s created twice", e.TaskID)
		}
		var nt NewTask
		if err := json.Unmarshal(e.Data, &nt); err != nil {
			return err
		}
		s.tasks[e.TaskID] = &Task{ID: e.TaskID, NewTask: nt, Revision: 1, Status: StatusQueued, CreatedBy: e.Actor, CreatedAt: e.At, UpdatedAt: e.At, Attempts: []Attempt{}}
		if n, err := strconv.Atoi(strings.TrimPrefix(e.TaskID, "T-")); err == nil && n >= s.nextID {
			s.nextID = n + 1
		}
	default:
		if err := s.applyLifecycle(e); err != nil {
			return err
		}
	}
	s.seq = e.Seq
	if e.IdempotencyKey != "" {
		s.byKey[e.IdempotencyKey] = e
	}
	s.events = append(s.events, e)
	return nil
}
