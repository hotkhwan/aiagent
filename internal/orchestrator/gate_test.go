package orchestrator

import (
	"context"
	"errors"
	"testing"

	"github.com/hotkhwan/aiagent/internal/taskstore"
)

type fakeBudget struct{ err error }

func (f fakeBudget) Admit(context.Context, string) error { return f.err }

// A provider that is out of budget takes no new work: the task stays ready and
// nothing is typed or recorded.
func TestAProviderOverBudgetGetsNoNewWork(t *testing.T) {
	r := newRig(t)
	task := r.create(t)
	r.o.Promote()
	r.o.Budget = fakeBudget{err: errors.New("claude quota is critical")}

	if _, err := r.o.Dispatch(context.Background(), task.ID); err == nil {
		t.Fatal("dispatch must be refused while the budget is exhausted")
	}
	if got := status(t, r, task.ID); got != taskstore.StatusReady {
		t.Fatalf("task is %s, want ready", got)
	}
	if len(r.agents.sent) != 0 {
		t.Fatalf("sent %d messages past the budget", len(r.agents.sent))
	}
	if got, _ := r.store.Get(task.ID); len(got.Attempts) != 0 {
		t.Fatalf("an attempt was recorded past the budget: %+v", got.Attempts)
	}
}

// Work reviewed against a base that has since moved goes back to verifying, so
// its tests run again before anyone can complete it.
func TestAMovedBaseSendsReviewedWorkBackToVerifying(t *testing.T) {
	r := newRig(t)
	r.agents.ws.TestCommand = []string{"go", "test", "./..."}
	r.git.testsOK = true
	r.git.changed = []string{"internal/world/outbox.go"}
	task := reported(t, r)
	r.o.Verify(context.Background())
	if status(t, r, task.ID) != taskstore.StatusAwaitingReview {
		t.Fatalf("setup: task is %s", status(t, r, task.ID))
	}

	moved, err := r.o.CheckBase(context.Background(), task.ID)
	if err != nil || moved {
		t.Fatalf("base unchanged: moved=%v err=%v", moved, err)
	}

	r.git.head = "b1c2d3e"
	moved, err = r.o.CheckBase(context.Background(), task.ID)
	if err != nil || !moved {
		t.Fatalf("base moved: moved=%v err=%v", moved, err)
	}
	if got := status(t, r, task.ID); got != taskstore.StatusVerifying {
		t.Fatalf("task is %s, want verifying", got)
	}
}
