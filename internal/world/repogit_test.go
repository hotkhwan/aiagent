package world

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gitRepo makes a repository with two commits: the first without a file
// named marker, the second adding it. It returns the dir and both shas.
func gitRepo(t *testing.T) (dir, first, second string) {
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
	writeFile(t, filepath.Join(dir, "a.txt"), "a\n")
	run("add", ".")
	run("commit", "-q", "-m", "first")
	first = run("rev-parse", "HEAD")
	writeFile(t, filepath.Join(dir, "marker"), "m\n")
	run("add", ".")
	run("commit", "-q", "-m", "second")
	second = run("rev-parse", "HEAD")
	run("checkout", "-q", first) // the operator's checkout sits elsewhere
	return dir, first, second
}

// Tests run at the agent's commit in a worktree of their own: they see that
// commit's files, the operator's checkout is left where it was, and the
// worktree is gone afterwards.
func TestRepoGitRunsTestsAtTheCommitInAWorktreeOfItsOwn(t *testing.T) {
	dir, first, second := gitRepo(t)
	g := RepoGit{R: ExecRunner{}}
	check := []string{"test", "-f", "marker"}

	run, err := g.RunTests(context.Background(), dir, second, check)
	if err != nil || !run.Passed {
		t.Fatalf("at the commit with the file: %+v, %v", run, err)
	}
	run, err = g.RunTests(context.Background(), dir, first, check)
	if err != nil || run.Passed {
		t.Fatalf("at the commit without it the run must fail, not error: %+v, %v", run, err)
	}

	if head, _ := g.Head(dir); head != first {
		t.Fatalf("the operator's checkout moved to %s", head)
	}
	out, _ := exec.Command("git", "-C", dir, "worktree", "list").Output()
	if n := len(strings.Split(strings.TrimSpace(string(out)), "\n")); n != 1 {
		t.Fatalf("a verify worktree was left behind:\n%s", out)
	}

	files, err := g.ChangedFiles(dir, first, second)
	if err != nil || len(files) != 1 || files[0] != "marker" {
		t.Fatalf("changed files = %v, %v", files, err)
	}
	if _, err := g.RunTests(context.Background(), dir, second, []string{"no-such-program-xyz"}); err == nil {
		t.Fatal("a test command that cannot start is an error, not a failed run")
	}
}
