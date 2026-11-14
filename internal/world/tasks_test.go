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

// Without a task log the task routes are absent, like everything else that is
// switched off.
func TestTaskRoutesAreAbsentWithoutAStore(t *testing.T) {
	e := newEnv(t)
	tok, _ := e.store.Enable("test")
	if code, _, _ := e.do(t, "GET", "/aiagent/api/tasks", tok, ""); code != 404 {
		t.Fatalf("want 404 with no task store, got %d", code)
	}
}
