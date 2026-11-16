package world

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hotkhwan/aiagent/internal/planner"
	"github.com/hotkhwan/aiagent/internal/taskstore"
)

// reviewRepo is a repository with a base commit and an agent's commit on top
// that edits one file, adds a test file and adds a binary.
func reviewRepo(t *testing.T) (dir, base, commit string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir = t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	run("init", "-q")
	writeFile(t, filepath.Join(dir, "outbox.go"), "package x\n\nfunc send() {}\n")
	run("add", ".")
	run("commit", "-q", "-m", "base")
	base = run("rev-parse", "HEAD")
	run("update-ref", "refs/remotes/origin/main", base)
	run("symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")

	writeFile(t, filepath.Join(dir, "outbox.go"), "package x\n\nfunc send() { ack() }\n\nfunc ack() {}\n")
	writeFile(t, filepath.Join(dir, "outbox_test.go"), "package x\n")
	if err := os.WriteFile(filepath.Join(dir, "logo.bin"), []byte{0, 1, 2, 0, 255}, 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-q", "-m", "agent work")
	commit = run("rev-parse", "HEAD")
	return dir, base, commit
}

// awaitingReview walks a task to awaitingReview with one reported attempt,
// the way the orchestrator would.
func awaitingReview(t *testing.T, ts *taskstore.Store, ws, base, commit string) (taskstore.Task, taskstore.Attempt) {
	t.Helper()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	task, err := ts.Create(taskstore.NewTask{ProjectID: "api", Title: "ยืนยันการรับข้อความ", Kind: "feature", AcceptanceCriteria: []string{"มี ack"}}, taskstore.ActorUser, "")
	must(err)
	_, err = ts.Transition(task.ID, taskstore.StatusReady, taskstore.ActorOrchestrator, taskstore.Change{}, "")
	must(err)
	_, a, err := ts.StartAttempt(task.ID, taskstore.AttemptStart{Provider: "claude", Workspace: ws, BaseCommit: base}, time.Hour, "")
	must(err)
	_, err = ts.MarkRunning(a.ID, "")
	must(err)
	_, err = ts.ReportAttempt(a.ID, commit, "done")
	must(err)
	_, err = ts.Transition(task.ID, taskstore.StatusVerifying, taskstore.ActorOrchestrator, taskstore.Change{Evidence: map[string]string{"commit": commit}}, "")
	must(err)
	task, err = ts.Transition(task.ID, taskstore.StatusAwaitingReview, taskstore.ActorOrchestrator,
		taskstore.Change{Evidence: map[string]string{"commit": commit, "tests": "passed: go test ./...", "needsHumanReview": "outbox_test.go"}}, "")
	must(err)
	return task, task.Attempts[0]
}

func reviewEnv(t *testing.T) (*env, string, taskstore.Task, string) {
	t.Helper()
	dir, base, commit := reviewRepo(t)
	e, tok := taskEnv(t)
	e.col.Runner = ExecRunner{}
	e.col.Workspaces = []workspaceEntry{{name: "api", session: "api", dir: dir, provider: "claude"}}
	task, _ := awaitingReview(t, e.svc.Tasks, "api", base, commit)
	return e, tok, task, base
}

// The review page is what the user decides from, so it carries what a person
// needs and nothing has to be taken on the agent's word: the files and how
// much each changed, the diff itself, which files are the kind work could use
// to pass itself, and what the work was based on.
func TestTaskReviewShowsTheDiffAndWhatToLookAt(t *testing.T) {
	e, tok, task, base := reviewEnv(t)
	code, m, _ := e.do(t, "GET", "/aiagent/api/tasks/"+task.ID+"/review", tok, "")
	if code != 200 {
		t.Fatalf("review: %d %v", code, m)
	}
	d, _ := m["details"].(map[string]any)

	files, _ := d["files"].([]any)
	got := map[string]map[string]any{}
	for _, f := range files {
		fm := f.(map[string]any)
		got[fm["path"].(string)] = fm
	}
	if len(got) != 3 {
		t.Fatalf("files = %v", files)
	}
	if f := got["outbox.go"]; f["added"] != float64(3) || f["deleted"] != float64(1) || f["protected"] == true {
		t.Errorf("outbox.go = %v", f)
	}
	if f := got["outbox_test.go"]; f["protected"] != true {
		t.Errorf("a test file should be marked protected: %v", f)
	}
	if f := got["logo.bin"]; f["binary"] != true {
		t.Errorf("a binary should be marked binary: %v", f)
	}

	diff, _ := d["diff"].(string)
	if !strings.Contains(diff, "+func ack() {}") || d["diffTruncated"] == true {
		t.Errorf("diff = %q (truncated %v)", diff, d["diffTruncated"])
	}
	b, _ := d["base"].(map[string]any)
	if b["commit"] != base || b["state"] != "current" || b["mainlineRef"] != "origin/main" {
		t.Errorf("base = %v", b)
	}
	tests, _ := d["tests"].(map[string]any)
	if tests["state"] != "passed" || tests["detail"] != "go test ./..." {
		t.Errorf("tests = %v", tests)
	}
	if scan, _ := d["scan"].(map[string]any); scan["state"] != "notScanned" {
		t.Errorf("no scanner runs yet, and the page must say so rather than look clean: %v", d["scan"])
	}
	if d["advisory"] != nil {
		t.Errorf("advice appeared before anyone asked: %v", d["advisory"])
	}
}

// When the checkout cannot be read the page says it does not know, instead
// of showing an empty diff that reads as "nothing changed".
func TestTaskReviewSaysUnknownWhenTheCheckoutIsGone(t *testing.T) {
	e, tok, task, _ := reviewEnv(t)
	e.col.Workspaces = nil
	code, m, _ := e.do(t, "GET", "/aiagent/api/tasks/"+task.ID+"/review", tok, "")
	if code != 200 {
		t.Fatalf("review: %d %v", code, m)
	}
	d, _ := m["details"].(map[string]any)
	if d["diffError"] == nil || d["diffError"] == "" {
		t.Fatalf("a review with no checkout must carry diffError: %v", d)
	}
	if b, _ := d["base"].(map[string]any); b["state"] != "unknown" {
		t.Errorf("base = %v, want unknown", d["base"])
	}

	code, _, _ = e.do(t, "GET", "/aiagent/api/tasks/T-9999/review", tok, "")
	if code != 404 {
		t.Errorf("a task that does not exist: %d, want 404", code)
	}
}

// A secret that reached the diff is not passed on to every open tab.
func TestTaskReviewRedactsTheDiff(t *testing.T) {
	if got := reviewDiffText([]byte("+token = \"sk-ant-api03-" + strings.Repeat("a", 40) + "\"\n")); strings.Contains(got, strings.Repeat("a", 40)) {
		t.Errorf("the diff still carries the key: %q", got)
	}
}

type fakePM struct {
	plan     planner.Plan
	advice   planner.Advice
	err      error
	planIn   planner.PlanInput
	reviewIn planner.ReviewInput
	block    chan struct{}
	panics   string
}

func (f *fakePM) Name() string { return "fake" }

func (f *fakePM) Plan(_ context.Context, in planner.PlanInput) (planner.Plan, planner.Call, error) {
	if f.block != nil {
		<-f.block
	}
	if f.panics != "" {
		panic(f.panics)
	}
	f.planIn = in
	p := f.plan
	if f.err == nil {
		// Round-trip, as the real adapter hands back a fresh value.
		b, _ := json.Marshal(f.plan)
		p = planner.Plan{}
		_ = json.Unmarshal(b, &p)
		if err := p.Check(in); err != nil {
			return planner.Plan{}, planner.Call{Outcome: "escalated", Tries: 3}, err
		}
	}
	return p, planner.Call{Adapter: "fake", Skill: "planProject", Version: "v1", Tries: 1, Outcome: "ok"}, f.err
}

func (f *fakePM) Advise(_ context.Context, in planner.ReviewInput) (planner.Advice, planner.Call, error) {
	if f.panics != "" {
		panic(f.panics)
	}
	f.reviewIn = in
	return f.advice, planner.Call{Adapter: "fake", Skill: "summarizeReview", Version: "v1", Tries: 1, Outcome: "ok"}, f.err
}

func withPM(t *testing.T, e *env, pm *fakePM) {
	t.Helper()
	th, err := planner.OpenThread(filepath.Join(e.dir, "state", "pm.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	e.svc.PM, e.svc.PMThread = pm, th
}

// A second opinion is asked for by the user, written by a model that sees the
// diff, and shown beside the decision as advice.
func TestTaskAdvisoryIsWrittenOnRequest(t *testing.T) {
	e, tok, task, _ := reviewEnv(t)
	pm := &fakePM{advice: planner.Advice{Summary: "เพิ่ม ack ใน outbox", Concerns: []planner.Concern{{Severity: "warn", File: "outbox_test.go", Note: "test ยังว่าง"}}}}
	withPM(t, e, pm)

	code, m, _ := e.do(t, "POST", "/aiagent/api/tasks/"+task.ID+"/advisory", tok, "{}")
	if code != 200 {
		t.Fatalf("advisory: %d %v", code, m)
	}
	e.svc.bg.Wait()

	if !strings.Contains(pm.reviewIn.Diff, "+func ack() {}") || len(pm.reviewIn.Files) != 3 || pm.reviewIn.Tests != "passed: go test ./..." {
		t.Errorf("the reviewer was shown: %+v", pm.reviewIn)
	}
	_, m, _ = e.do(t, "GET", "/aiagent/api/tasks/"+task.ID+"/review", tok, "")
	adv, _ := m["details"].(map[string]any)["advisory"].(map[string]any)
	if adv["state"] != "done" {
		t.Fatalf("advisory = %v", adv)
	}
	if a, _ := adv["advice"].(map[string]any); a["summary"] != "เพิ่ม ack ใน outbox" {
		t.Errorf("advice = %v", adv["advice"])
	}
	if _, has := adv["verdict"]; has {
		t.Errorf("advice must not carry a verdict")
	}
	b, _ := os.ReadFile(e.auditFP)
	if !strings.Contains(string(b), `"rpc":"aiagent.taskAdvisory"`) {
		t.Error("asking for advice must be audited")
	}
	if got, _ := e.svc.Tasks.Get(task.ID); got.Status != taskstore.StatusAwaitingReview {
		t.Errorf("advice moved the task to %s; only the user decides", got.Status)
	}
}

// The note is written behind the response, where nothing else would catch a
// crash. It ends as a failed note the user can ask for again, and the service
// goes on serving.
func TestATaskAdvisoryThatPanicsIsAFailedNote(t *testing.T) {
	e, tok, task, _ := reviewEnv(t)
	withPM(t, e, &fakePM{panics: "index out of range [3] with length 2"})

	code, m, _ := e.do(t, "POST", "/aiagent/api/tasks/"+task.ID+"/advisory", tok, "{}")
	if code != 200 {
		t.Fatalf("advisory: %d %v", code, m)
	}
	e.svc.bg.Wait()

	_, m, _ = e.do(t, "GET", "/aiagent/api/tasks/"+task.ID+"/review", tok, "")
	adv, _ := m["details"].(map[string]any)["advisory"].(map[string]any)
	if adv["state"] != "failed" {
		t.Fatalf("advisory after a crash = %v, want failed", adv)
	}
	if why, _ := adv["error"].(string); why == "" || strings.Contains(why, "index out of range") {
		t.Errorf("error shown = %q; it should say the call crashed without the panic text", why)
	}
}

// Without a PM configured there is no advice to ask for.
func TestTaskAdvisoryIsAbsentWithoutAPM(t *testing.T) {
	e, tok, task, _ := reviewEnv(t)
	code, _, _ := e.do(t, "POST", "/aiagent/api/tasks/"+task.ID+"/advisory", tok, "{}")
	if code != 404 {
		t.Fatalf("advisory without a PM: %d, want 404", code)
	}
}
