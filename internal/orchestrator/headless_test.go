package orchestrator

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/hotkhwan/aiagent/internal/taskstore"
)

// fakeHeadless stands in for the headless worker: it records what it was asked
// to start and reports what the worker finished with.
type fakeHeadless struct {
	ws       Workspace
	startErr error
	started  []taskstore.Attempt
	prompts  []string
	running  bool
	outcome  Result
	done     bool
}

func (f *fakeHeadless) Workspace() Workspace { return f.ws }

func (f *fakeHeadless) Start(_ context.Context, a taskstore.Attempt, _ string, prompt string) error {
	if f.startErr != nil {
		return f.startErr
	}
	f.started = append(f.started, a)
	f.prompts = append(f.prompts, prompt)
	return nil
}

func (f *fakeHeadless) Running(string) bool { return f.running }

func (f *fakeHeadless) Outcome(string) (Result, bool) { return f.outcome, f.done }

func headlessRig(t *testing.T) (*rig, *fakeHeadless) {
	t.Helper()
	r := newRig(t)
	h := &fakeHeadless{ws: Workspace{Name: HeadlessWorkspace, Provider: "claude", Dir: r.dir}, running: true}
	r.o.Headless = h
	return r, h
}

func TestHeadlessDispatchStartsAWorkerAndDoesNotTypeIntoAPane(t *testing.T) {
	r, h := headlessRig(t)
	task := r.create(t)
	r.o.Promote()

	a, err := r.o.Dispatch(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(h.started) != 1 || h.started[0].ID != a.ID {
		t.Fatalf("worker started = %+v, want attempt %s", h.started, a.ID)
	}
	if !strings.Contains(h.prompts[0], task.ID) || !strings.Contains(h.prompts[0], a.ID) {
		t.Fatalf("prompt does not name the task and attempt: %q", h.prompts[0])
	}
	if len(r.agents.sent) != 0 {
		t.Fatalf("headless dispatch typed into a pane: %q", r.agents.sent)
	}
	got, _ := r.store.Get(task.ID)
	if got.Status != taskstore.StatusRunning || got.Attempts[0].Workspace != HeadlessWorkspace {
		t.Fatalf("task = %s, attempt workspace = %q; want running on %s", got.Status, got.Attempts[0].Workspace, HeadlessWorkspace)
	}
}

func TestHeadlessWithoutAWorkerCLIBlocksTheTaskAsMissingCLI(t *testing.T) {
	r, h := headlessRig(t)
	h.startErr = ErrWorkerMissing
	task := r.create(t)
	r.o.Promote()

	if _, err := r.o.Dispatch(context.Background(), task.ID); !errors.Is(err, ErrWorkerMissing) {
		t.Fatalf("err = %v, want ErrWorkerMissing", err)
	}
	got, _ := r.store.Get(task.ID)
	if got.Status != taskstore.StatusBlocked || !strings.Contains(got.BlockedReason, "missingCli") {
		t.Fatalf("task = %s %q; want blocked naming missingCli", got.Status, got.BlockedReason)
	}
}

func TestHeadlessStartThatFailsRequeuesTheTask(t *testing.T) {
	r, h := headlessRig(t)
	h.startErr = errors.New("git worktree: branch exists")
	task := r.create(t)
	r.o.Promote()

	if _, err := r.o.Dispatch(context.Background(), task.ID); err == nil {
		t.Fatal("a failed start must be reported")
	}
	if status(t, r, task.ID) != taskstore.StatusQueued {
		t.Fatalf("task = %s; want queued after a failed start", status(t, r, task.ID))
	}
}

func TestHeadlessDoneResultMovesTheTaskToVerifying(t *testing.T) {
	r, h := headlessRig(t)
	task := r.create(t)
	r.o.Promote()
	if _, err := r.o.Dispatch(context.Background(), task.ID); err != nil {
		t.Fatal(err)
	}

	h.running = false
	h.done = true
	h.outcome = Result{Status: "done", Commit: "3f2a1c9", Summary: "แก้แล้ว"}
	r.o.Tick(context.Background())

	if status(t, r, task.ID) != taskstore.StatusVerifying {
		t.Fatalf("task = %s; want verifying after a done result", status(t, r, task.ID))
	}
}

func TestHeadlessDoneResultNamingAMissingCommitStaysRunning(t *testing.T) {
	r, h := headlessRig(t)
	task := r.create(t)
	r.o.Promote()
	if _, err := r.o.Dispatch(context.Background(), task.ID); err != nil {
		t.Fatal(err)
	}

	h.running = false
	h.done = true
	h.outcome = Result{Status: "done", Commit: "deadbee"}
	r.o.Tick(context.Background())

	if status(t, r, task.ID) != taskstore.StatusRunning {
		t.Fatalf("task = %s; a commit that does not exist must not count", status(t, r, task.ID))
	}
}

func TestHeadlessBlockedResultFailsTheTaskWithItsReason(t *testing.T) {
	r, h := headlessRig(t)
	task := r.create(t)
	r.o.Promote()
	if _, err := r.o.Dispatch(context.Background(), task.ID); err != nil {
		t.Fatal(err)
	}

	h.running = false
	h.done = true
	h.outcome = Result{Status: "blocked", Summary: "ต้องการสิทธิ์ push"}
	r.o.Tick(context.Background())

	got, _ := r.store.Get(task.ID)
	if got.Status != taskstore.StatusFailed || !strings.Contains(got.Attempts[0].ExitReason, "ต้องการสิทธิ์ push") {
		t.Fatalf("task = %s, attempt reason %q; want failed with the agent's reason", got.Status, got.Attempts[0].ExitReason)
	}
}

func TestHeadlessStillRunningKeepsTheAttemptRunning(t *testing.T) {
	r, _ := headlessRig(t)
	task := r.create(t)
	r.o.Promote()
	if _, err := r.o.Dispatch(context.Background(), task.ID); err != nil {
		t.Fatal(err)
	}

	r.o.Tick(context.Background())

	if status(t, r, task.ID) != taskstore.StatusRunning {
		t.Fatalf("task = %s; a live worker must keep its task running", status(t, r, task.ID))
	}
}
