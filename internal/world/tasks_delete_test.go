package world

import (
	"testing"
	"time"
)

// Only a cancelled task can be deleted through the API, and it is gone from
// the list afterwards. A task that is still queued is refused.
func TestADeletedTaskLeavesTheTaskListThroughTheAPI(t *testing.T) {
	e, tok := taskEnv(t)
	if code, m, _ := e.do(t, "POST", "/aiagent/api/tasks", tok, newTaskBody); code != 200 {
		t.Fatalf("create: %d %v", code, m)
	}
	if code, _, _ := e.do(t, "POST", "/aiagent/api/tasks/T-0001/delete", tok, ""); code == 200 {
		t.Fatal("a queued task was deleted")
	}
	time.Sleep(writeSpacing)
	cancel := `{"to":"cancelled","reason":"ไม่ต้องทำแล้ว"}`
	if code, m, _ := e.do(t, "POST", "/aiagent/api/tasks/T-0001/transition", tok, cancel); code != 200 {
		t.Fatalf("cancel: %d %v", code, m)
	}
	time.Sleep(writeSpacing)
	if code, m, _ := e.do(t, "POST", "/aiagent/api/tasks/T-0001/delete", tok, ""); code != 200 {
		t.Fatalf("delete a cancelled task: %d %v", code, m)
	}
	time.Sleep(writeSpacing)
	code, m, _ := e.do(t, "GET", "/aiagent/api/tasks", tok, "")
	if code != 200 {
		t.Fatalf("list: %d %v", code, m)
	}
	d, _ := m["details"].(map[string]any)
	if tasks, _ := d["tasks"].([]any); len(tasks) != 0 {
		t.Fatalf("a deleted task is still listed: %v", tasks)
	}
}
