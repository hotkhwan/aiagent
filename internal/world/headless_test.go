package world

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/hotkhwan/aiagent/internal/orchestrator"
	"github.com/hotkhwan/aiagent/internal/taskstore"
)

func initHeadlessRepo(t *testing.T) (dir, base string) {
	t.Helper()
	dir = t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		full := append([]string{"-C", dir, "-c", "user.name=test", "-c", "user.email=test@example.invalid"}, args...)
		out, err := exec.Command("git", full...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q")
	git("commit", "--allow-empty", "-qm", "base")
	return dir, git("rev-parse", "HEAD")
}

func fakeClaude(t *testing.T, body string) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

func headlessFor(t *testing.T, repo, bin string) *Headless {
	t.Helper()
	return &Headless{Repo: repo, Bin: bin, StateDir: t.TempDir(), Home: t.TempDir()}
}

func waitRun(t *testing.T, h *Headless, id string) {
	t.Helper()
	h.mu.Lock()
	run := h.runs[id]
	h.mu.Unlock()
	if run == nil {
		t.Fatalf("no run for %s", id)
	}
	<-run.done
}

func TestHeadlessMissingCLIIsReportedBeforeAnyWorktreeExists(t *testing.T) {
	repo, base := initHeadlessRepo(t)
	t.Setenv("PATH", t.TempDir())
	h := &Headless{Repo: repo, StateDir: t.TempDir(), Home: t.TempDir()}

	err := h.Start(context.Background(), taskstore.Attempt{ID: "T-0001-A1"}, base, "prompt")
	if err != orchestrator.ErrWorkerMissing {
		t.Fatalf("err = %v, want ErrWorkerMissing", err)
	}
	if _, err := os.Stat(filepath.Join(repo, ".worktrees", "T-0001-A1")); !os.IsNotExist(err) {
		t.Fatalf("a worktree was created for a worker that cannot run: %v", err)
	}
}

// doneClaudeScript commits one file, then reports done with that commit.
const doneClaudeScript = `prompt=$(cat)
id=$(printf '%s' "$prompt" | head -n 1 | sed 's/.* attempt \([^ ]*\) .*/\1/')
echo work > work.txt
git add work.txt && git commit -qm "work $id" || exit 3
sha=$(git rev-parse HEAD)
mkdir -p .aiagent/results
printf '{"status":"done","commit":"%s","summary":"ok"}\n' "$sha" > ".aiagent/results/$id.json"
`

func TestHeadlessWorkerCommitsWhenTheRepositoryIsALinkedWorktree(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("sandbox-exec is macOS only")
	}
	main, base := initHeadlessRepo(t)
	linked := filepath.Join(t.TempDir(), "linked")
	if out, err := exec.Command("git", "-C", main, "worktree", "add", "-q", "--detach", linked, base).CombinedOutput(); err != nil {
		t.Fatalf("worktree add: %v\n%s", err, out)
	}
	h := headlessFor(t, linked, fakeClaude(t, doneClaudeScript))
	a := taskstore.Attempt{ID: "T-0004-A1"}

	if err := h.Start(context.Background(), a, base, "[aiagent task T-0004 · attempt T-0004-A1 · revision 1]\ndo it\n"); err != nil {
		t.Fatal(err)
	}
	waitRun(t, h, a.ID)

	r, ok := h.Outcome(a.ID)
	if !ok || r.Status != "done" || r.Commit == "" || r.Commit == base {
		t.Fatalf("outcome = %+v, %v; a linked-worktree repository must still get a new commit", r, ok)
	}
	if out, err := exec.Command("git", "-C", main, "cat-file", "-e", r.Commit+"^{commit}").CombinedOutput(); err != nil {
		t.Fatalf("commit is not in the shared repository: %v\n%s", err, out)
	}
}

func TestHeadlessWorkerCommitsAndReportsDone(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("sandbox-exec is macOS only")
	}
	repo, base := initHeadlessRepo(t)
	bin := fakeClaude(t, doneClaudeScript)
	h := headlessFor(t, repo, bin)
	a := taskstore.Attempt{ID: "T-0001-A1"}

	if err := h.Start(context.Background(), a, base, "[aiagent task T-0001 · attempt T-0001-A1 · revision 1]\ndo it\n"); err != nil {
		t.Fatal(err)
	}
	waitRun(t, h, a.ID)

	r, ok := h.Outcome(a.ID)
	if !ok || r.Status != "done" || r.Commit == "" {
		t.Fatalf("outcome = %+v, %v; want done with a commit", r, ok)
	}
	if out, err := exec.Command("git", "-C", repo, "cat-file", "-e", r.Commit+"^{commit}").CombinedOutput(); err != nil {
		t.Fatalf("reported commit is not in the repository: %v\n%s", err, out)
	}
	if out, err := exec.Command("git", "-C", repo, "rev-parse", "--verify", "aiagent/"+a.ID).CombinedOutput(); err != nil {
		t.Fatalf("attempt branch missing: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(repo, ".worktrees", a.ID)); !os.IsNotExist(err) {
		t.Fatalf("a finished worktree with commits should be removed: %v", err)
	}
}

func TestHeadlessWorkerCannotRewriteTheRepositoryConfig(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("sandbox-exec is macOS only")
	}
	repo, base := initHeadlessRepo(t)
	bin := fakeClaude(t, `cat > /dev/null
echo work > work.txt
git add work.txt && git commit -qm work
sha=$(git rev-parse HEAD)
if echo "[core]
	fsmonitor = /tmp/evil" >> "`+repo+`/.git/config" 2>/dev/null; then guard=config-write-allowed; else guard=config-write-refused; fi
mkdir -p .aiagent/results
printf '{"status":"done","commit":"%s","summary":"%s"}\n' "$sha" "$guard" > ".aiagent/results/T-0003-A1.json"
`)
	h := headlessFor(t, repo, bin)
	a := taskstore.Attempt{ID: "T-0003-A1"}

	if err := h.Start(context.Background(), a, base, "prompt"); err != nil {
		t.Fatal(err)
	}
	waitRun(t, h, a.ID)

	r, _ := h.Outcome(a.ID)
	if r.Summary != "config-write-refused" {
		t.Fatalf("worker summary = %q; the worker must not write .git/config", r.Summary)
	}
}

func TestHeadlessWorkerWithoutAResultIsBlockedAndKeepsItsWorktree(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("sandbox-exec is macOS only")
	}
	repo, base := initHeadlessRepo(t)
	bin := fakeClaude(t, "cat > /dev/null\necho 'no result written' >&2\nexit 1\n")
	h := headlessFor(t, repo, bin)
	a := taskstore.Attempt{ID: "T-0002-A1"}

	if err := h.Start(context.Background(), a, base, "prompt"); err != nil {
		t.Fatal(err)
	}
	waitRun(t, h, a.ID)

	r, ok := h.Outcome(a.ID)
	if !ok || r.Status != "blocked" || !strings.Contains(r.Summary, "without a result") {
		t.Fatalf("outcome = %+v, %v; want blocked naming the missing result", r, ok)
	}
	if _, err := os.Stat(filepath.Join(repo, ".worktrees", a.ID)); err != nil {
		t.Fatalf("the worktree of a blocked worker must stay for inspection: %v", err)
	}
}

func TestVerifyRunWithholdsTheSubscriptionToken(t *testing.T) {
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "secret-value")
	out, err := ExecRunner{}.RunTestIn(context.Background(), t.TempDir(), []string{"sh", "-c", "echo token=${CLAUDE_CODE_OAUTH_TOKEN:-absent}"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "token=absent") {
		t.Fatalf("the code under test saw the subscription token: %q", out)
	}
}

func TestHeadlessWorkerGetsItsOwnTempDirectory(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("sandbox-exec is macOS only")
	}
	repo, base := initHeadlessRepo(t)
	bin := fakeClaude(t, `cat > /dev/null
mkdir -p .aiagent/results
printf '{"status":"blocked","summary":"%s"}\n' "$TMPDIR" > ".aiagent/results/T-0005-A1.json"
`)
	h := headlessFor(t, repo, bin)
	a := taskstore.Attempt{ID: "T-0005-A1"}

	if err := h.Start(context.Background(), a, base, "prompt"); err != nil {
		t.Fatal(err)
	}
	waitRun(t, h, a.ID)

	r, _ := h.Outcome(a.ID)
	if !strings.Contains(r.Summary, "aiagent-worker-") {
		t.Fatalf("worker TMPDIR = %q; want the per-run directory the sandbox allows", r.Summary)
	}
}

func TestHeadlessWorkerCommitsWithoutGitWarnings(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("sandbox-exec is macOS only")
	}
	repo, base := initHeadlessRepo(t)
	bin := fakeClaude(t, `cat > /dev/null
echo work > work.txt
git add work.txt
git commit -qm work 2> "$TMPDIR/commit.err" || exit 3
guard=clean
[ -s "$TMPDIR/commit.err" ] && guard=dirty
sha=$(git rev-parse HEAD)
mkdir -p .aiagent/results
printf '{"status":"done","commit":"%s","summary":"%s"}\n' "$sha" "$guard" > ".aiagent/results/T-0006-A1.json"
`)
	h := headlessFor(t, repo, bin)
	a := taskstore.Attempt{ID: "T-0006-A1"}

	if err := h.Start(context.Background(), a, base, "prompt"); err != nil {
		t.Fatal(err)
	}
	waitRun(t, h, a.ID)

	r, _ := h.Outcome(a.ID)
	if r.Summary != "clean" {
		t.Fatalf("worker commit printed a warning; the sandbox must allow the ref files git needs")
	}
}

func TestHeadlessWorkerCannotReadTheOperatorKeys(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("sandbox-exec is macOS only")
	}
	repo, base := initHeadlessRepo(t)
	home := t.TempDir()
	secret := filepath.Join(home, "littledays-keys", "claude-worker.env")
	if err := os.MkdirAll(filepath.Dir(secret), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(secret, []byte("CLAUDE_CODE_OAUTH_TOKEN=placeholder\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	bin := fakeClaude(t, `cat > /dev/null
guard=key-read-allowed
if cat '`+secret+`' >/dev/null 2>&1; then guard=key-read-allowed; else guard=key-read-refused; fi
mkdir -p .aiagent/results
printf '{"status":"blocked","summary":"%s"}\n' "$guard" > ".aiagent/results/T-0007-A1.json"
`)
	h := &Headless{Repo: repo, Bin: bin, StateDir: t.TempDir(), Home: home}
	a := taskstore.Attempt{ID: "T-0007-A1"}

	if err := h.Start(context.Background(), a, base, "prompt"); err != nil {
		t.Fatal(err)
	}
	waitRun(t, h, a.ID)

	r, _ := h.Outcome(a.ID)
	if r.Summary != "key-read-refused" {
		t.Fatalf("worker summary = %q; the worker must not read the operator's key directory", r.Summary)
	}
}
