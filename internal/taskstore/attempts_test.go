package taskstore

import (
	"testing"
	"time"
)

const lease = 5 * time.Minute

func readied(t *testing.T, s *Store, nt NewTask) Task {
	t.Helper()
	task, err := s.Create(nt, ActorUser, "")
	if err != nil {
		t.Fatal(err)
	}
	if task, err = s.Transition(task.ID, StatusReady, ActorOrchestrator, Change{}, ""); err != nil {
		t.Fatal(err)
	}
	return task
}

func start(t *testing.T, s *Store, taskID string) Attempt {
	t.Helper()
	_, a, err := s.StartAttempt(taskID, AttemptStart{Provider: "claude", Workspace: "lit", BaseCommit: "9a79130"}, lease, "")
	if err != nil {
		t.Fatalf("start attempt: %v", err)
	}
	return a
}

// Starting an attempt is what puts a task in running, with a lease the
// worker has to keep renewing.
func TestStartingAnAttemptRunsTheTaskUnderALease(t *testing.T) {
	s, _ := newStore(t)
	task := readied(t, s, outboxTask())
	a := start(t, s, task.ID)

	got, _ := s.Get(task.ID)
	if got.Status != StatusRunning || len(got.Attempts) != 1 {
		t.Fatalf("task after start: %+v", got)
	}
	if a.ID != "T-0001-A1" || a.Status != AttemptStarting || !a.LeaseExpiresAt.Equal(t0.Add(lease)) {
		t.Fatalf("attempt = %+v", a)
	}
	if _, _, err := s.StartAttempt(task.ID, AttemptStart{}, lease, ""); err == nil {
		t.Fatal("a running task cannot get a second attempt")
	}
}

// One writer per repository: two agents patching the same checkout at once
// is how a patch is verified against code that is not what gets merged.
func TestOneProjectHasOneLiveAttemptAtATime(t *testing.T) {
	s, _ := newStore(t)
	first := readied(t, s, outboxTask())
	second := readied(t, s, outboxTask())
	a := start(t, s, first.ID)

	if _, _, err := s.StartAttempt(second.ID, AttemptStart{}, lease, ""); err == nil {
		t.Fatal("a second attempt on the same project must wait")
	}
	other := outboxTask()
	other.ProjectID = "littleDays"
	third := readied(t, s, other)
	start(t, s, third.ID) // another project is not blocked

	if _, err := s.EndAttempt(a.ID, AttemptFailed, "หมดเวลา", ""); err != nil {
		t.Fatal(err)
	}
	start(t, s, second.ID)
}

// The whole road to done, with every gate: the attempt must end before the
// task moves on, verifying needs a commit, review needs test evidence, and
// only the user completes. A revision request starts the next revision.
func TestTheRoadToCompletedHasAGateAtEveryStep(t *testing.T) {
	s, _ := newStore(t)
	task := readied(t, s, outboxTask())
	a := start(t, s, task.ID)

	if _, err := s.Transition(task.ID, StatusVerifying, ActorOrchestrator, Change{Evidence: map[string]string{"commit": "3f2a1c9"}}, ""); err == nil {
		t.Fatal("a task moved on while its attempt still held it")
	}
	if _, err := s.EndAttempt(a.ID, AttemptReported, "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Transition(task.ID, StatusVerifying, ActorOrchestrator, Change{}, ""); err == nil {
		t.Fatal("verifying without a commit to verify")
	}
	steps := []struct {
		to    Status
		actor Actor
		ch    Change
	}{
		{StatusVerifying, ActorOrchestrator, Change{Evidence: map[string]string{"commit": "3f2a1c9"}}},
		{StatusAwaitingReview, ActorOrchestrator, Change{Evidence: map[string]string{"tests": "43/43 ผ่าน"}}},
		{StatusQueued, ActorUser, Change{Reason: "ขอ test กรณี restart เพิ่ม"}},
	}
	for _, st := range steps {
		if _, err := s.Transition(task.ID, st.to, st.actor, st.ch, ""); err != nil {
			t.Fatalf("to %s: %v", st.to, err)
		}
	}
	got, _ := s.Get(task.ID)
	if got.Revision != 2 {
		t.Fatalf("a revision request should start revision 2; got %d", got.Revision)
	}
	if _, err := s.Transition(task.ID, StatusCompleted, ActorOrchestrator, Change{}, ""); err == nil {
		t.Fatal("the orchestrator completed a task")
	}
}

// A lease that runs out is reported, not acted on: the old worker may still
// be typing, so it has to be stopped before the task is given to anyone else.
func TestAnExpiredLeaseIsReportedAndTheTaskIsNotRequeued(t *testing.T) {
	s, _ := newStore(t)
	task := readied(t, s, outboxTask())
	a := start(t, s, task.ID)

	s.Now = func() time.Time { return t0.Add(4 * time.Minute) }
	if _, err := s.Heartbeat(a.ID); err != nil {
		t.Fatal(err)
	}
	if got := s.Expired(t0.Add(6 * time.Minute)); len(got) != 0 {
		t.Fatalf("a renewed lease expired early: %+v", got)
	}
	got := s.Expired(t0.Add(10 * time.Minute))
	if len(got) != 1 || got[0].ID != a.ID {
		t.Fatalf("expired = %+v, want %s", got, a.ID)
	}
	if tk, _ := s.Get(task.ID); tk.Status != StatusRunning {
		t.Fatalf("an expired lease must not requeue by itself; task is %s", tk.Status)
	}
}

// The service stops after recording a dispatch but before confirming the
// agent received it. On the next start nobody knows whether the text was
// typed, so the attempt is unknown — still holding the task and the project —
// and nothing is dispatched again until it is reconciled.
func TestARestartBeforeTheDispatchIsConfirmedLeavesTheAttemptUnknown(t *testing.T) {
	s, path := newStore(t)
	task := readied(t, s, outboxTask())
	start(t, s, task.ID)

	s2 := openAt(t, path)
	if n, err := s2.Recover(); err != nil || n != 1 {
		t.Fatalf("recover: %d, %v", n, err)
	}
	got, _ := s2.Get(task.ID)
	if got.Status != StatusRunning || got.Attempts[0].Status != AttemptUnknown {
		t.Fatalf("after recovery: task %s, attempt %s", got.Status, got.Attempts[0].Status)
	}
	again := readied(t, s2, outboxTask())
	if _, _, err := s2.StartAttempt(again.ID, AttemptStart{}, lease, ""); err == nil {
		t.Fatal("an unknown attempt must still hold the project")
	}
	if n, _ := s2.Recover(); n != 0 {
		t.Fatalf("recovering twice changed %d more attempts", n)
	}
}

// Workers report through the orchestrator; they do not start or end their
// own attempts.
func TestOnlyTheOrchestratorStartsAndEndsAttempts(t *testing.T) {
	s, _ := newStore(t)
	task := readied(t, s, outboxTask())
	a := start(t, s, task.ID)
	if _, err := s.endAttemptAs(a.ID, AttemptReported, "", "", ActorWorker); err == nil {
		t.Fatal("a worker ended its own attempt")
	}
}
