package world

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestAJobWhoseConversationMovedIsAttachedByItsJobID: after /clear a job keeps
// its jobId (3ef9360e) while its session id changes (13c52b46-…). The session
// id's prefix names no job, so `claude attach 13c52b46` failed and every
// message to the admin session on ata-core answered 502.
func TestAJobWhoseConversationMovedIsAttachedByItsJobID(t *testing.T) {
	const sid = "13c52b46-8701-43f2-9eeb-01d89932b63d"
	e := newEnv(t)
	e.now = time.Now()
	e.runner.agentsJSON = `[{"id":"3ef9360e","sessionId":"` + sid + `","name":"admin","kind":"background","state":"done"}]`
	writeFile(t, filepath.Join(e.home, ".claude", "projects", "-root", sid+".jsonl"),
		`{"type":"user","timestamp":"2026-11-04T03:00:00Z","message":{"content":"go"}}`+"\n")
	e.runner.trustOnAttach = true
	if _, err := e.col.SendToSession(context.Background(), sid, "ทีมทำอะไรไปแล้วบ้าง"); err != nil {
		t.Fatalf("send: %v", err)
	}
	starts := relayStarts(e)
	if len(starts) != 1 || !strings.HasSuffix(starts[0], " claude attach 3ef9360e") {
		t.Fatalf("relay starts = %q, want one claude attach 3ef9360e", starts)
	}
	if !strings.Contains(starts[0], "aiagent-3ef9360e") {
		t.Fatalf("relay named after the session id, not the job: %q", starts[0])
	}
}
