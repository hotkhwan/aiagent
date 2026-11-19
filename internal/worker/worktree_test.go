package worker

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// newRepo is a temp repository with one commit, returning its path and HEAD.
func newRepo(t *testing.T) (string, string) {
	t.Helper()
	repo := t.TempDir()
	git(t, repo, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "add", "README.md")
	git(t, repo, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "init")
	return repo, git(t, repo, "rev-parse", "HEAD")
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestAttemptWorktreeLeavesTheMainCheckoutAlone(t *testing.T) {
	repo, base := newRepo(t)
	wt := filepath.Join(t.TempDir(), "A1")
	branch := "aiagent/t1/t1-A1"

	if err := AddWorktree(context.Background(), repo, base, branch, wt); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(wt, "README.md")); err != nil {
		t.Fatalf("worktree not checked out: %v", err)
	}
	if got := git(t, repo, "status", "--porcelain"); got != "" {
		t.Fatalf("main checkout dirty after worktree add: %q", got)
	}
	if got := git(t, repo, "branch", "--show-current"); got != "main" {
		t.Fatalf("main checkout moved to %q", got)
	}
}

// An attempt that failed with nothing committed leaves no directory and no
// branch behind, and the main checkout has no trace of it.
func TestCleanupRemovesAnAttemptWithNoCommits(t *testing.T) {
	repo, base := newRepo(t)
	wt := filepath.Join(t.TempDir(), "A1")
	branch := "aiagent/t1/t1-A1"
	if err := AddWorktree(context.Background(), repo, base, branch, wt); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, "half.go"), []byte("package x\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := Cleanup(context.Background(), repo, wt, branch, base); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Fatalf("worktree dir survived cleanup: %v", err)
	}
	if got := git(t, repo, "branch", "--list", branch); got != "" {
		t.Fatalf("empty attempt branch survived: %q", got)
	}
	if got := git(t, repo, "status", "--porcelain"); got != "" {
		t.Fatalf("main checkout dirty: %q", got)
	}
}

// Committed work is never deleted with its attempt: the branch is kept so the
// reviewer can still read it.
func TestCleanupKeepsTheBranchWhenThereIsCommittedWork(t *testing.T) {
	repo, base := newRepo(t)
	wt := filepath.Join(t.TempDir(), "A1")
	branch := "aiagent/t1/t1-A1"
	if err := AddWorktree(context.Background(), repo, base, branch, wt); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, "x.go"), []byte("package x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, wt, "add", "x.go")
	git(t, wt, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "work")

	if err := Cleanup(context.Background(), repo, wt, branch, base); err != nil {
		t.Fatal(err)
	}
	if got := git(t, repo, "branch", "--list", branch); got == "" {
		t.Fatal("a branch with commits was deleted")
	}
}

func TestWorktreeArgumentsCannotBecomeFlags(t *testing.T) {
	repo, base := newRepo(t)
	wt := filepath.Join(t.TempDir(), "A1")
	if err := AddWorktree(context.Background(), repo, base, "--force", wt); err == nil {
		t.Fatal("a branch name that starts with - must be refused")
	}
	if err := AddWorktree(context.Background(), repo, "HEAD; rm -rf /", "aiagent/t/x", wt); err == nil {
		t.Fatal("a base that is not a commit SHA must be refused")
	}
}
