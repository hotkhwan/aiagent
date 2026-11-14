package orchestrator

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hotkhwan/aiagent/internal/taskstore"
)

var t0 = time.Date(2026, 11, 14, 8, 0, 0, 0, time.UTC)

// fakeAgents stands in for the tmux sessions of claude, codex and gemini.
type fakeAgents struct {
	ws       Workspace
	pickErr  error
	sendErr  error
	acked    bool
	sent     []string
	activity time.Time
}

func (f *fakeAgents) Pick(_ context.Context, provider string) (Workspace, error) {
	if f.pickErr != nil {
		return Workspace{}, f.pickErr
	}
	return f.ws, nil
}

func (f *fakeAgents) Send(_ context.Context, ws Workspace, text string) (Delivery, error) {
	if f.sendErr != nil {
		return Delivery{}, f.sendErr
	}
	f.sent = append(f.sent, text)
	return Delivery{SessionID: "sess-1", Acked: f.acked}, nil
}

func (f *fakeAgents) Lookup(name string) (Workspace, bool) { return f.ws, name == f.ws.Name }

func (f *fakeAgents) LastActivity(_ context.Context, sessionID string) (time.Time, bool) {
	return f.activity, !f.activity.IsZero()
}

// fakeGit knows one head and the commits that exist.
type fakeGit struct {
	head    string
	commits map[string]bool
}

func (g *fakeGit) Head(string) (string, error)         { return g.head, nil }
func (g *fakeGit) HasCommit(_ string, sha string) bool { return g.commits[sha] }

type rig struct {
	o      *Orchestrator
	store  *taskstore.Store
	agents *fakeAgents
	git    *fakeGit
	dir    string
	now    time.Time
}

func newRig(t *testing.T) *rig {
	t.Helper()
	tmp := t.TempDir()
	st, err := taskstore.Open(filepath.Join(tmp, "tasks.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	r := &rig{store: st, dir: filepath.Join(tmp, "repo"), now: t0}
	st.Now = func() time.Time { return r.now }
	r.agents = &fakeAgents{ws: Workspace{Name: "lit", Provider: "claude", Dir: r.dir}, acked: true}
	r.git = &fakeGit{head: "9a79130", commits: map[string]bool{"3f2a1c9": true}}
	r.o = &Orchestrator{Store: st, Agents: r.agents, Git: r.git, Lease: 45 * time.Minute, Now: func() time.Time { return r.now }}
	return r
}

func (r *rig) create(t *testing.T, blockedBy ...string) taskstore.Task {
	t.Helper()
	task, err := r.store.Create(taskstore.NewTask{
		ProjectID: "aiagent", Title: "แก้ outbox ไม่ให้ส่งซ้ำ",
		AcceptanceCriteria: []string{"restart แล้วไม่ส่งซ้ำ", "มี test ครอบคลุม"},
		AllowedPaths:       []string{"internal/world/**"},
		BlockedBy:          blockedBy,
	}, taskstore.ActorUser, "")
	if err != nil {
		t.Fatal(err)
	}
	return task
}

func status(t *testing.T, r *rig, id string) taskstore.Status {
	t.Helper()
	task, _ := r.store.Get(id)
	return task.Status
}

// A task is ready once everything it waits on is completed, and blocked —
// with the reason — as soon as one of them can no longer complete.
func TestPromoteReadiesTasksWhoseDependenciesAreDone(t *testing.T) {
	r := newRig(t)
	dep := r.create(t)
	waits := r.create(t, dep.ID)
	free := r.create(t)

	r.o.Promote()
	if status(t, r, free.ID) != taskstore.StatusReady || status(t, r, dep.ID) != taskstore.StatusReady {
		t.Fatal("tasks with nothing to wait for should be ready")
	}
	if status(t, r, waits.ID) != taskstore.StatusQueued {
		t.Fatal("a task must wait for its dependency")
	}

	if _, err := r.store.Transition(dep.ID, taskstore.StatusCancelled, taskstore.ActorUser, taskstore.Change{Reason: "ไม่ทำ"}, ""); err != nil {
		t.Fatal(err)
	}
	r.o.Promote()
	got, _ := r.store.Get(waits.ID)
	if got.Status != taskstore.StatusBlocked || !strings.Contains(got.BlockedReason, dep.ID) {
		t.Fatalf("a task whose dependency was cancelled should be blocked naming it; got %s %q", got.Status, got.BlockedReason)
	}
}

// Dispatching types one self-contained instruction into the chosen agent's
// session. The attempt is recorded before anything is typed, and runs once the
// agent's transcript shows the text arrived.
func TestDispatchSendsTheTaskAndRunsItOnceDeliveryIsAcked(t *testing.T) {
	r := newRig(t)
	task := r.create(t)
	r.o.Promote()

	a, err := r.o.Dispatch(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if a.Status != taskstore.AttemptRunning || a.SessionID != "sess-1" || a.BaseCommit != "9a79130" || a.Workspace != "lit" {
		t.Fatalf("attempt = %+v", a)
	}
	if len(r.agents.sent) != 1 {
		t.Fatalf("sent %d messages, want 1", len(r.agents.sent))
	}
	msg := r.agents.sent[0]
	for _, want := range []string{task.ID, a.ID, "restart แล้วไม่ส่งซ้ำ", "internal/world/**", ".aiagent/results/" + a.ID + ".json", "aiagent/" + task.ID + "/" + a.ID} {
		if !strings.Contains(msg, want) {
			t.Errorf("the dispatch should mention %q:\n%s", want, msg)
		}
	}
	if len([]rune(msg)) > 4000 {
		t.Errorf("the dispatch is %d characters; the session takes at most 4000", len([]rune(msg)))
	}
}

// Text typed that never showed up in the transcript leaves the attempt
// unknown, holding the task, until someone confirms it.
func TestADispatchThatIsNotAckedIsUnknown(t *testing.T) {
	r := newRig(t)
	r.agents.acked = false
	task := r.create(t)
	r.o.Promote()
	a, err := r.o.Dispatch(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if a.Status != taskstore.AttemptUnknown {
		t.Fatalf("attempt = %s, want unknown", a.Status)
	}
	if _, err := r.o.Dispatch(context.Background(), task.ID); err == nil {
		t.Fatal("an unknown dispatch must not be sent again")
	}
}

// When the text could not be typed at all, nothing reached the agent: the
// attempt failed and the task goes back to the queue with the reason.
func TestAFailedSendReturnsTheTaskToTheQueue(t *testing.T) {
	r := newRig(t)
	r.agents.sendErr = errors.New("the agent window is running zsh")
	task := r.create(t)
	r.o.Promote()
	if _, err := r.o.Dispatch(context.Background(), task.ID); err == nil {
		t.Fatal("a send that failed must be reported")
	}
	got, _ := r.store.Get(task.ID)
	if got.Status != taskstore.StatusQueued || got.Attempts[0].Status != taskstore.AttemptFailed {
		t.Fatalf("after a failed send: task %s, attempt %s", got.Status, got.Attempts[0].Status)
	}
}

func dispatched(t *testing.T, r *rig) (taskstore.Task, taskstore.Attempt) {
	t.Helper()
	task := r.create(t)
	r.o.Promote()
	a, err := r.o.Dispatch(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	return task, a
}

func writeResult(t *testing.T, r *rig, attemptID, body string) {
	t.Helper()
	p := filepath.Join(r.dir, ".aiagent", "results", attemptID+".json")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Activity in the agent's transcript renews the lease; a quiet agent does not.
func TestTickRenewsTheLeaseOnTranscriptActivity(t *testing.T) {
	r := newRig(t)
	_, a := dispatched(t, r)
	r.now = t0.Add(30 * time.Minute)
	r.agents.activity = t0.Add(29 * time.Minute)
	r.o.Tick(context.Background())
	if exp := r.store.Expired(t0.Add(50 * time.Minute)); len(exp) != 0 {
		t.Fatalf("activity should have renewed the lease: %+v", exp)
	}
	r.now = t0.Add(80 * time.Minute)
	r.o.Tick(context.Background())
	if exp := r.store.Expired(t0.Add(80 * time.Minute)); len(exp) != 1 || exp[0].ID != a.ID {
		t.Fatalf("with no new activity the lease should run out: %+v", exp)
	}
}

// The agent reports through a result file in the workspace. That file is the
// repository's content, so it is not believed: the commit it names has to
// exist before the task moves to verifying.
func TestAReportedResultIsCheckedAgainstGitBeforeVerifying(t *testing.T) {
	r := newRig(t)
	task, a := dispatched(t, r)

	writeResult(t, r, a.ID, `{"status":"done","commit":"deadbee","summary":"เสร็จแล้ว"}`)
	r.o.Tick(context.Background())
	if status(t, r, task.ID) != taskstore.StatusRunning {
		t.Fatal("a result naming a commit that does not exist must not move the task")
	}

	writeResult(t, r, a.ID, `{"status":"done","commit":"3f2a1c9","summary":"เสร็จแล้ว"}`)
	r.o.Tick(context.Background())
	got, _ := r.store.Get(task.ID)
	if got.Status != taskstore.StatusVerifying || got.Attempts[0].Status != taskstore.AttemptReported {
		t.Fatalf("after a real commit: task %s, attempt %s", got.Status, got.Attempts[0].Status)
	}
}

// An agent that cannot go on says so in its result, and the task stops there
// with its question for the user rather than spinning.
func TestAnAgentThatIsBlockedFailsTheAttemptWithItsReason(t *testing.T) {
	r := newRig(t)
	task, _ := dispatched(t, r)
	got, _ := r.store.Get(task.ID)
	writeResult(t, r, got.Attempts[0].ID, `{"status":"blocked","summary":"ไม่รู้ว่าต้องเก็บ error ไว้ที่ไหน"}`)
	r.o.Tick(context.Background())
	got, _ = r.store.Get(task.ID)
	if got.Status != taskstore.StatusFailed || !strings.Contains(got.Attempts[0].ExitReason, "ไม่รู้ว่าต้องเก็บ") {
		t.Fatalf("blocked agent: task %s, reason %q", got.Status, got.Attempts[0].ExitReason)
	}
}
