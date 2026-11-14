package taskstore

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

var t0 = time.Date(2026, 11, 14, 8, 0, 0, 0, time.UTC)

func openAt(t *testing.T, path string) *Store {
	t.Helper()
	s, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	s.Now = func() time.Time { return t0 }
	return s
}

func newStore(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "tasks.jsonl")
	return openAt(t, path), path
}

func outboxTask() NewTask {
	return NewTask{
		ProjectID:          "aiagent",
		Title:              "แก้ outbox ไม่ให้ส่งซ้ำ",
		Kind:               "bug",
		AcceptanceCriteria: []string{"restart แล้วไม่ส่งซ้ำ", "มี test ครอบคลุม"},
		AllowedPaths:       []string{"internal/world/**"},
	}
}

// A task is the unit the PM plans and the user approves, so it must outlive
// the service: everything about it is read back from the log after a restart.
func TestACreatedTaskIsReadBackAfterReopening(t *testing.T) {
	s, path := newStore(t)
	got, err := s.Create(outboxTask(), ActorUser, "")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "T-0001" || got.Status != StatusQueued || got.Revision != 1 {
		t.Fatalf("new task = %+v; want T-0001, queued, revision 1", got)
	}

	s2 := openAt(t, path)
	back, ok := s2.Get("T-0001")
	if !ok || back.Title != "แก้ outbox ไม่ให้ส่งซ้ำ" || len(back.AcceptanceCriteria) != 2 || back.Status != StatusQueued {
		t.Fatalf("after reopen: %+v, %v", back, ok)
	}
	next, err := s2.Create(outboxTask(), ActorUser, "")
	if err != nil || next.ID != "T-0002" {
		t.Fatalf("ids must keep counting after a reopen: %q, %v", next.ID, err)
	}
}

// A task with no project or no acceptance criteria cannot be verified, so it
// cannot be completed honestly; it is refused at the door.
func TestATaskNeedsAProjectAndAcceptanceCriteria(t *testing.T) {
	s, _ := newStore(t)
	noCriteria := outboxTask()
	noCriteria.AcceptanceCriteria = nil
	if _, err := s.Create(noCriteria, ActorUser, ""); err == nil {
		t.Error("a task without acceptance criteria must be refused")
	}
	noProject := outboxTask()
	noProject.ProjectID = ""
	if _, err := s.Create(noProject, ActorUser, ""); err == nil {
		t.Error("a task without a project must be refused")
	}
}

// A phone that retries a request after a timeout must not create the task
// twice: the same idempotency key gets the same task back.
func TestTheSameIdempotencyKeyCreatesOneTask(t *testing.T) {
	s, _ := newStore(t)
	a, err := s.Create(outboxTask(), ActorUser, "req-1")
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Create(outboxTask(), ActorUser, "req-1")
	if err != nil {
		t.Fatal(err)
	}
	if a.ID != b.ID || len(s.List()) != 1 {
		t.Fatalf("one key, one task: got %s and %s, %d tasks", a.ID, b.ID, len(s.List()))
	}
}

// The service and the CLI each open the log. Their writes interleave without
// clashing ids, and each sees the other's tasks.
func TestTwoOpenersShareOneLog(t *testing.T) {
	a, path := newStore(t)
	b := openAt(t, path)
	ta, err := a.Create(outboxTask(), ActorUser, "")
	if err != nil {
		t.Fatal(err)
	}
	tb, err := b.Create(outboxTask(), ActorUser, "")
	if err != nil {
		t.Fatal(err)
	}
	if ta.ID == tb.ID {
		t.Fatalf("two openers handed out the same id %s", ta.ID)
	}
	if _, err := a.Transition(tb.ID, StatusReady, ActorOrchestrator, Change{}, ""); err != nil {
		t.Fatalf("the first opener could not act on the second's task: %v", err)
	}
	if got, _ := b.Get(tb.ID); got.Status != StatusReady {
		t.Fatalf("the second opener did not see the first's change: %s", got.Status)
	}
}

// A crash in the middle of an append leaves half a line at the end of the
// log. Everything before it is intact and must load, and the store must keep
// writing whole lines after it.
func TestATornLastLineDoesNotLoseEarlierTasks(t *testing.T) {
	s, path := newStore(t)
	if _, err := s.Create(outboxTask(), ActorUser, ""); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString(`{"seq":2,"type":"taskCre`)
	f.Close()

	s2 := openAt(t, path)
	if _, ok := s2.Get("T-0001"); !ok {
		t.Fatal("the task written before the torn line was lost")
	}
	if _, err := s2.Create(outboxTask(), ActorUser, ""); err != nil {
		t.Fatalf("writing after a torn line: %v", err)
	}
	s3 := openAt(t, path)
	if len(s3.List()) != 2 {
		t.Fatalf("after recovery and one more write, want 2 tasks; got %d", len(s3.List()))
	}
}
