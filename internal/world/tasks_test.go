package world

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hotkhwan/aiagent/internal/taskstore"
)

func taskEnv(t *testing.T) (*env, string) {
	t.Helper()
	e := newEnv(t)
	tok, _ := e.store.Enable("test")
	ts, err := taskstore.Open(filepath.Join(e.dir, "state", "tasks.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	e.svc.Tasks = ts
	return e, tok
}

const newTaskBody = `{"projectId":"aiagent","title":"แก้ outbox ไม่ให้ส่งซ้ำ","kind":"bug",` +
	`"acceptanceCriteria":["restart แล้วไม่ส่งซ้ำ"],"idempotencyKey":"req-1"}`

// The board and the PM read tasks through the API, in camelCase, and every
// write leaves the same audit trail as any other action.
func TestTasksAreCreatedAndReadThroughTheAPI(t *testing.T) {
	e, tok := taskEnv(t)
	code, m, _ := e.do(t, "POST", "/aiagent/api/tasks", tok, newTaskBody)
	if code != 200 {
		t.Fatalf("create: %d %v", code, m)
	}
	d, _ := m["details"].(map[string]any)
	if d["taskId"] != "T-0001" || d["status"] != "queued" || d["taskRevision"] != float64(1) {
		t.Fatalf("created task = %v", d)
	}

	code, m, _ = e.do(t, "GET", "/aiagent/api/tasks", tok, "")
	if code != 200 {
		t.Fatalf("list: %d %v", code, m)
	}
	list, _ := m["details"].(map[string]any)["tasks"].([]any)
	if len(list) != 1 {
		t.Fatalf("list = %v", m)
	}

	code, m, _ = e.do(t, "GET", "/aiagent/api/tasks/T-0001", tok, "")
	if code != 200 {
		t.Fatalf("get: %d %v", code, m)
	}
	if ev, _ := m["details"].(map[string]any)["events"].([]any); len(ev) != 1 {
		t.Fatalf("a task's history should come with it: %v", m)
	}

	b, _ := os.ReadFile(e.auditFP)
	if !strings.Contains(string(b), `"rpc":"aiagent.taskCreate"`) {
		t.Fatal("creating a task must be audited")
	}
}

// The API speaks for the user, so it can make the user's moves — cancel,
// approve, ask for a revision — and none of the orchestrator's.
func TestTheAPIMovesTasksOnlyAsTheUser(t *testing.T) {
	e, tok := taskEnv(t)
	if code, m, _ := e.do(t, "POST", "/aiagent/api/tasks", tok, newTaskBody); code != 200 {
		t.Fatalf("create: %d %v", code, m)
	}
	time.Sleep(writeSpacing)
	if code, m, _ := e.do(t, "POST", "/aiagent/api/tasks/T-0001/transition", tok, `{"to":"ready"}`); code != 409 {
		t.Fatalf("readying is the orchestrator's move, the API must refuse it: %d %v", code, m)
	}
	time.Sleep(writeSpacing)
	code, m, _ := e.do(t, "POST", "/aiagent/api/tasks/T-0001/transition", tok, `{"to":"cancelled","reason":"ไม่ต้องทำแล้ว"}`)
	if code != 200 {
		t.Fatalf("cancel: %d %v", code, m)
	}
	if d, _ := m["details"].(map[string]any); d["status"] != "cancelled" {
		t.Fatalf("after cancel: %v", d)
	}
	time.Sleep(writeSpacing)
	if code, _, _ := e.do(t, "POST", "/aiagent/api/tasks/T-9999/transition", tok, `{"to":"cancelled"}`); code != 404 {
		t.Fatalf("an unknown task must be a 404, got %d", code)
	}
}

// The user approves a dispatch; the orchestrator picks the workspace whose
// pane runs the task's provider and types the envelope into it. With no
// transcript showing it arrived the attempt is unknown, and the user's
// confirmation is what makes it run.
func TestApprovingADispatchTypesTheTaskIntoTheAgent(t *testing.T) {
	e, tok := taskEnv(t)
	e.col.Workspaces = routeWorkspaces()
	e.runner.sessions = "lit\t1793750000\nlit-codex\t1793750000\n"
	e.runner.panes = "lit\tagent\t1\tclaude\nlit-codex\tagent\t1\tcodex\n"
	e.runner.gitHead = "9a79130"
	e.svc.Orch = NewOrchestrator(e.svc.Tasks, e.col, time.Hour)

	body := `{"projectId":"aiagent","title":"แก้ outbox","provider":"codex","acceptanceCriteria":["ไม่ส่งซ้ำ"]}`
	if code, m, _ := e.do(t, "POST", "/aiagent/api/tasks", tok, body); code != 200 {
		t.Fatalf("create: %d %v", code, m)
	}
	e.svc.Orch.Promote()
	time.Sleep(writeSpacing)
	code, m, _ := e.do(t, "POST", "/aiagent/api/tasks/T-0001/dispatch", tok, "")
	if code != 200 {
		t.Fatalf("dispatch: %d %v", code, m)
	}
	a, _ := m["details"].(map[string]any)
	if a["status"] != "unknown" || a["workspace"] != "lit-codex" || a["baseCommit"] != "9a79130" {
		t.Fatalf("attempt = %v", a)
	}
	calls := e.runner.agcmdCalls()
	if len(calls) != 1 || calls[0][0] != "dispatch" || calls[0][1] != "lit-codex" || !strings.Contains(calls[0][2], "T-0001-A1") {
		t.Fatalf("agcmd calls = %q", calls)
	}

	time.Sleep(writeSpacing)
	code, m, _ = e.do(t, "POST", "/aiagent/api/tasks/T-0001/attempts/T-0001-A1/confirm", tok, `{}`)
	if code != 200 {
		t.Fatalf("confirm: %d %v", code, m)
	}
	if a, _ := m["details"].(map[string]any); a["status"] != "running" {
		t.Fatalf("after confirming: %v", a)
	}
	time.Sleep(writeSpacing)
	if code, _, _ := e.do(t, "POST", "/aiagent/api/tasks/T-0001/dispatch", tok, ""); code != 409 {
		t.Fatalf("a running task must not be dispatched again, got %d", code)
	}
}

// Without a task log the task routes are absent, like everything else that is
// switched off.
func TestTaskRoutesAreAbsentWithoutAStore(t *testing.T) {
	e := newEnv(t)
	tok, _ := e.store.Enable("test")
	if code, _, _ := e.do(t, "GET", "/aiagent/api/tasks", tok, ""); code != 404 {
		t.Fatalf("want 404 with no task store, got %d", code)
	}
}

// Completing reviewed work checks the base first: when HEAD has moved since
// the attempt started, the completion is refused and the work goes back to
// verifying; when it has not, the user's completion goes through.
func TestCompletionWaitsForAnUnchangedBase(t *testing.T) {
	e, tok := taskEnv(t)
	e.col.Workspaces = routeWorkspaces()
	e.runner.sessions = "lit\t1793750000\nlit-codex\t1793750000\n"
	e.runner.panes = "lit\tagent\t1\tclaude\nlit-codex\tagent\t1\tcodex\n"
	e.runner.gitHead = "9a79130"
	e.svc.Orch = NewOrchestrator(e.svc.Tasks, e.col, time.Hour)

	body := `{"projectId":"aiagent","title":"แก้ outbox","provider":"codex","acceptanceCriteria":["ไม่ส่งซ้ำ"]}`
	if code, m, _ := e.do(t, "POST", "/aiagent/api/tasks", tok, body); code != 200 {
		t.Fatalf("create: %d %v", code, m)
	}
	e.svc.Orch.Promote()
	time.Sleep(writeSpacing)
	if code, m, _ := e.do(t, "POST", "/aiagent/api/tasks/T-0001/dispatch", tok, ""); code != 200 {
		t.Fatalf("dispatch: %d %v", code, m)
	}
	time.Sleep(writeSpacing)
	if code, m, _ := e.do(t, "POST", "/aiagent/api/tasks/T-0001/attempts/T-0001-A1/confirm", tok, `{}`); code != 200 {
		t.Fatalf("confirm: %d %v", code, m)
	}
	ts := e.svc.Tasks
	if _, err := ts.ReportAttempt("T-0001-A1", "9a79130", "done"); err != nil {
		t.Fatal(err)
	}
	if _, err := ts.Transition("T-0001", taskstore.StatusVerifying, taskstore.ActorOrchestrator, taskstore.Change{Evidence: map[string]string{"commit": "9a79130"}}, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := ts.Transition("T-0001", taskstore.StatusAwaitingReview, taskstore.ActorOrchestrator, taskstore.Change{Evidence: map[string]string{"tests": "passed"}}, ""); err != nil {
		t.Fatal(err)
	}

	e.runner.gitHead = "b1c2d3e"
	time.Sleep(writeSpacing)
	if code, m, _ := e.do(t, "POST", "/aiagent/api/tasks/T-0001/transition", tok, `{"to":"completed"}`); code != 409 {
		t.Fatalf("completing over a moved base must be refused, got %d %v", code, m)
	}
	if task, _ := ts.Get("T-0001"); task.Status != taskstore.StatusVerifying {
		t.Fatalf("after a moved base the task is %s, want verifying", task.Status)
	}
}
