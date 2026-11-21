package taskstore

import "testing"

// Only a cancelled task can be deleted. Deleting takes it off the board and
// out of the PM's view, but its history stays in the log.
func TestOnlyACancelledTaskCanBeDeleted(t *testing.T) {
	s, _ := newStore(t)
	task := created(t, s)
	if err := s.Delete(task.ID, ActorUser); err == nil {
		t.Fatal("a queued task must not be deletable")
	}
	if _, err := s.Transition(task.ID, StatusCancelled, ActorUser, Change{Reason: "ไม่ต้องทำแล้ว"}, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(task.ID, ActorOrchestrator); err == nil {
		t.Error("only the user may delete a task")
	}
	if err := s.Delete(task.ID, ActorUser); err != nil {
		t.Fatalf("deleting a cancelled task: %v", err)
	}
	if _, ok := s.Get(task.ID); ok {
		t.Error("a deleted task is still readable")
	}
	for _, got := range s.List() {
		if got.ID == task.ID {
			t.Error("a deleted task is still listed")
		}
	}
	if len(s.Events(task.ID)) == 0 {
		t.Error("deleting must keep the task's history")
	}
	if err := s.Delete(task.ID, ActorUser); err == nil {
		t.Error("a second delete must be refused")
	}
}

// The deletion is a log event, so a restart replays it and the task stays gone.
func TestADeletedTaskStaysDeletedAfterReopen(t *testing.T) {
	s, path := newStore(t)
	task := created(t, s)
	if _, err := s.Transition(task.ID, StatusCancelled, ActorUser, Change{Reason: "x"}, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(task.ID, ActorUser); err != nil {
		t.Fatal(err)
	}
	if _, ok := openAt(t, path).Get(task.ID); ok {
		t.Error("after a restart the deleted task came back")
	}
}
