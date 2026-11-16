package taskstore

import "testing"

func created(t *testing.T, s *Store) Task {
	t.Helper()
	task, err := s.Create(outboxTask(), ActorUser, "")
	if err != nil {
		t.Fatal(err)
	}
	return task
}

// Who may move a task where is data, not scattered ifs: the orchestrator
// readies work, the PM and the workers only propose and report.
func TestOnlyTheOrchestratorReadiesATask(t *testing.T) {
	s, _ := newStore(t)
	task := created(t, s)
	for _, a := range []Actor{ActorPM, ActorWorker} {
		if _, err := s.Transition(task.ID, StatusReady, a, Change{}, ""); err == nil {
			t.Errorf("%s must not be able to ready a task", a)
		}
	}
	got, err := s.Transition(task.ID, StatusReady, ActorOrchestrator, Change{}, "")
	if err != nil || got.Status != StatusReady {
		t.Fatalf("orchestrator readying: %+v, %v", got, err)
	}
}

// No shortcut to done: nobody, the user included, can complete a task that
// was never verified and reviewed.
func TestATaskCannotSkipToCompleted(t *testing.T) {
	s, _ := newStore(t)
	task := created(t, s)
	before := len(s.Events(task.ID))
	for _, a := range []Actor{ActorUser, ActorOrchestrator, ActorPM, ActorWorker} {
		if _, err := s.Transition(task.ID, StatusCompleted, a, Change{}, ""); err == nil {
			t.Errorf("%s completed a queued task", a)
		}
	}
	if after := len(s.Events(task.ID)); after != before {
		t.Fatalf("a refused transition must leave nothing in the log: %d events, was %d", after, before)
	}
}

// Cancelling is the user's call, and a cancelled task stays cancelled.
func TestOnlyTheUserCancelsAndCancelledIsFinal(t *testing.T) {
	s, _ := newStore(t)
	task := created(t, s)
	if _, err := s.Transition(task.ID, StatusCancelled, ActorOrchestrator, Change{Reason: "x"}, ""); err == nil {
		t.Error("the orchestrator must not cancel the user's task")
	}
	if _, err := s.Transition(task.ID, StatusCancelled, ActorUser, Change{Reason: "ไม่ต้องทำแล้ว"}, ""); err != nil {
		t.Fatal(err)
	}
	for _, to := range []Status{StatusQueued, StatusReady} {
		if _, err := s.Transition(task.ID, to, ActorUser, Change{Reason: "x"}, ""); err == nil {
			t.Errorf("a cancelled task came back as %s", to)
		}
	}
}

// A blocked task says why, so the board can show what it is waiting for.
func TestBlockingNeedsAReason(t *testing.T) {
	s, _ := newStore(t)
	task := created(t, s)
	if _, err := s.Transition(task.ID, StatusBlocked, ActorOrchestrator, Change{}, ""); err == nil {
		t.Fatal("blocking without a reason must be refused")
	}
	got, err := s.Transition(task.ID, StatusBlocked, ActorOrchestrator, Change{Reason: "quota"}, "")
	if err != nil || got.BlockedReason != "quota" {
		t.Fatalf("blocked: %+v, %v", got, err)
	}
	got, err = s.Transition(task.ID, StatusQueued, ActorUser, Change{}, "")
	if err != nil || got.Status != StatusQueued || got.BlockedReason != "" {
		t.Fatalf("unblocking: %+v, %v", got, err)
	}
}

// The same request twice moves the task once.
func TestARepeatedTransitionKeyIsAppliedOnce(t *testing.T) {
	s, _ := newStore(t)
	task := created(t, s)
	if _, err := s.Transition(task.ID, StatusReady, ActorOrchestrator, Change{}, "k-ready"); err != nil {
		t.Fatal(err)
	}
	before := len(s.Events(task.ID))
	got, err := s.Transition(task.ID, StatusReady, ActorOrchestrator, Change{}, "k-ready")
	if err != nil || got.Status != StatusReady {
		t.Fatalf("a repeated key should return the current task: %+v, %v", got, err)
	}
	if after := len(s.Events(task.ID)); after != before {
		t.Fatalf("a repeated key wrote %d more events", after-before)
	}
}
