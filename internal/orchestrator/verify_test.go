package orchestrator

import (
	"context"
	"path"
	"strings"
	"testing"

	"github.com/hotkhwan/aiagent/internal/taskstore"
)

// reported brings a task to verifying: dispatched, and the agent's result
// names a commit that exists.
func reported(t *testing.T, r *rig) taskstore.Task {
	t.Helper()
	task, a := dispatched(t, r)
	writeResult(t, r, a.ID, `{"status":"done","commit":"3f2a1c9","summary":"เสร็จ"}`)
	r.o.Tick(context.Background())
	if status(t, r, task.ID) != taskstore.StatusVerifying {
		t.Fatal("setup: task did not reach verifying")
	}
	return task
}

func lastEvidence(t *testing.T, r *rig, id string) string {
	t.Helper()
	evs := r.store.Events(id)
	return string(evs[len(evs)-1].Data)
}

// The tests that decide whether work passes are the operator's, from the
// workspace config, run at the agent's commit — never a command the agent
// could have edited. Passing work waits for the user with the evidence.
func TestVerifyRunsTheConfiguredTestsAtTheReportedCommit(t *testing.T) {
	r := newRig(t)
	r.agents.ws.TestCommand = []string{"go", "test", "./..."}
	r.git.testsOK = true
	r.git.changed = []string{"internal/world/outbox.go"}
	task := reported(t, r)

	r.o.Verify(context.Background())
	if status(t, r, task.ID) != taskstore.StatusAwaitingReview {
		t.Fatalf("passing work should wait for the user; task is %s", status(t, r, task.ID))
	}
	if r.git.testedAt != "3f2a1c9" || strings.Join(r.git.testArgv, " ") != "go test ./..." {
		t.Fatalf("ran %q at %s", r.git.testArgv, r.git.testedAt)
	}
	if ev := lastEvidence(t, r, task.ID); !strings.Contains(ev, "passed") {
		t.Fatalf("the evidence should say the tests passed: %s", ev)
	}
}

// Failing tests send the work back with the reason, not to the user.
func TestFailingTestsSendTheTaskBack(t *testing.T) {
	r := newRig(t)
	r.agents.ws.TestCommand = []string{"go", "test", "./..."}
	r.git.changed = []string{"internal/world/outbox.go"}
	task := reported(t, r)

	r.o.Verify(context.Background())
	got, _ := r.store.Get(task.ID)
	if got.Status != taskstore.StatusQueued {
		t.Fatalf("failing tests should requeue; task is %s", got.Status)
	}
	if ev := lastEvidence(t, r, task.ID); !strings.Contains(ev, "TestOutbox") {
		t.Fatalf("the reason should carry the failure: %s", ev)
	}
}

// Work that touched files outside what the task allowed is out of scope,
// whatever its tests say.
func TestChangesOutsideTheAllowedPathsAreSentBack(t *testing.T) {
	r := newRig(t)
	r.agents.ws.TestCommand = []string{"go", "test", "./..."}
	r.git.testsOK = true
	r.git.changed = []string{"internal/world/outbox.go", "cmd/aiagent/main.go"}
	task := reported(t, r)

	r.o.Verify(context.Background())
	if status(t, r, task.ID) != taskstore.StatusQueued {
		t.Fatalf("an out-of-scope change should requeue; task is %s", status(t, r, task.ID))
	}
	if ev := lastEvidence(t, r, task.ID); !strings.Contains(ev, "cmd/aiagent/main.go") {
		t.Fatalf("the reason should name the file: %s", ev)
	}
}

// Touching tests, CI or agent configuration is how a worker could make its
// own work pass. It is not refused — sometimes it is the task — but the user
// is told, in the evidence they review.
func TestTouchingTestsOrCIIsFlaggedForTheUser(t *testing.T) {
	r := newRig(t)
	r.agents.ws.TestCommand = []string{"go", "test", "./..."}
	r.git.testsOK = true
	r.git.changed = []string{"internal/world/outbox.go", "internal/world/outbox_test.go"}
	task := reported(t, r)

	r.o.Verify(context.Background())
	if status(t, r, task.ID) != taskstore.StatusAwaitingReview {
		t.Fatalf("task is %s", status(t, r, task.ID))
	}
	if ev := lastEvidence(t, r, task.ID); !strings.Contains(ev, "needsHumanReview") || !strings.Contains(ev, "outbox_test.go") {
		t.Fatalf("the evidence should flag the test file: %s", ev)
	}
}

// Scope and review flags are decided on paths alone; these are the cases the
// verdicts above rest on.
func TestAllowedPathsAndProtectedFiles(t *testing.T) {
	scope := []string{"internal/world/**", "web/*.js"}
	for p, want := range map[string]bool{
		"internal/world/outbox.go":     true,
		"internal/world/sub/x.go":      true,
		"internal/worldx/y.go":         false,
		"web/app.js":                   true,
		"web/assets/a.png":             false,
		"cmd/aiagent/main.go":          false,
		"internal/world/../../etc/pwd": false,
	} {
		if got := allowed(path.Clean(p), scope); got != want {
			t.Errorf("allowed(%q) = %v, want %v", p, got, want)
		}
	}
	if !allowed("anything/at/all.go", nil) {
		t.Error("no allowed paths means the whole repository")
	}
	for p, want := range map[string]bool{
		"internal/world/outbox_test.go": true,
		".github/workflows/ci.yml":      true,
		".claude/settings.json":         true,
		"CLAUDE.md":                     true,
		"docs/CLAUDE.md":                true,
		"go.mod":                        true,
		"web/app.spec.js":               true,
		"internal/world/outbox.go":      false,
		"docs/README.md":                false,
	} {
		if got := isProtected(p); got != want {
			t.Errorf("isProtected(%q) = %v, want %v", p, got, want)
		}
	}
}

// With no test command configured nothing was run, and the evidence says so
// plainly instead of reading as a pass.
func TestWithNoTestCommandTheEvidenceSaysNothingRan(t *testing.T) {
	r := newRig(t)
	r.git.changed = []string{"internal/world/outbox.go"}
	task := reported(t, r)

	r.o.Verify(context.Background())
	if status(t, r, task.ID) != taskstore.StatusAwaitingReview {
		t.Fatalf("task is %s", status(t, r, task.ID))
	}
	if ev := lastEvidence(t, r, task.ID); !strings.Contains(ev, "notRun") {
		t.Fatalf("the evidence must say no tests ran: %s", ev)
	}
}
