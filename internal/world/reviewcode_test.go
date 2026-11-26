package world

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitT(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// codeRepo makes a checkout with develop and a feature branch one commit
// ahead of it, and returns the checkout and the develop commit.
func codeRepo(t *testing.T, dir string) (string, string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	repo := filepath.Join(dir, "work", "proj")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	gitT(t, repo, "init", "-q", "-b", "develop")
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitT(t, repo, "add", ".")
	gitT(t, repo, "commit", "-q", "-m", "base")
	base := gitT(t, repo, "rev-parse", "HEAD")
	gitT(t, repo, "checkout", "-q", "-b", "feat/x")
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("one\ntwo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "b.txt"), []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitT(t, repo, "add", ".")
	gitT(t, repo, "commit", "-q", "-m", "feature")
	return repo, base
}

// TestDetectReviewCode: from a folder inside the checkout it finds the top,
// the branch, HEAD and the merge base with develop; on develop itself it
// falls back to HEAD's parent; outside a checkout it fails.
func TestDetectReviewCode(t *testing.T) {
	dir := t.TempDir()
	repo, base := codeRepo(t, dir)
	if err := os.MkdirAll(filepath.Join(repo, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	g := RepoGit{R: ExecRunner{}}
	code, err := DetectReviewCode(g, filepath.Join(repo, "sub"))
	if err != nil {
		t.Fatal(err)
	}
	head := gitT(t, repo, "rev-parse", "HEAD")
	if code.Repo != repo || code.Branch != "feat/x" || code.Commit != head || code.Base != base || code.BaseRef != "develop" {
		t.Fatalf("code = %+v (want base %s head %s)", code, base, head)
	}

	gitT(t, repo, "checkout", "-q", "develop")
	gitT(t, repo, "merge", "-q", "--ff-only", "feat/x")
	code, err = DetectReviewCode(g, repo)
	if err != nil || code.Base != base || code.BaseRef != "HEAD^" {
		t.Fatalf("on develop: %+v %v", code, err)
	}

	if _, err := DetectReviewCode(g, dir); err == nil {
		t.Fatal("a folder outside any checkout was accepted")
	}
}

// TestReviewCodeTab: a page filed with a code reference serves its diff; a
// reference outside the home or with a bad commit is dropped on filing; a
// page without one says why there is no diff.
func TestReviewCodeTab(t *testing.T) {
	e := newEnv(t)
	tok, _ := e.store.Enable("test")
	e.col.Runner = ExecRunner{}
	repo, _ := codeRepo(t, e.home)
	code, err := DetectReviewCode(RepoGit{R: ExecRunner{}}, repo)
	if err != nil {
		t.Fatal(err)
	}
	r, err := FileReview(e.home, "feature x", "", "", "", repo, code, []byte("<h2>ข้อ 1</h2>"), e.now)
	if err != nil || r.Code == nil {
		t.Fatalf("filed %+v %v", r, err)
	}

	status, m, _ := e.do(t, "GET", "/aiagent/api/reviews/"+r.ID+"/code", tok, "")
	d, _ := m["details"].(map[string]any)
	if status != 200 || d == nil {
		t.Fatalf("code route: %d %v", status, m)
	}
	if d["error"] != nil {
		t.Fatalf("error: %v", d["error"])
	}
	diff, _ := d["diff"].(string)
	if !strings.Contains(diff, "+two") || !strings.Contains(diff, "b.txt") {
		t.Fatalf("diff = %q", diff)
	}
	if files, _ := d["files"].([]any); len(files) != 2 {
		t.Fatalf("files = %v", d["files"])
	}
	if d["branchHead"] != nil {
		t.Fatalf("branch has not moved, got branchHead %v", d["branchHead"])
	}

	// the branch moves on after the page was filed
	if err := os.WriteFile(filepath.Join(repo, "c.txt"), []byte("later\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitT(t, repo, "add", ".")
	gitT(t, repo, "commit", "-q", "-m", "later")
	v, err := e.col.ReviewCodeDiff(r.ID)
	if err != nil || v.BranchHead == "" || len(v.Files) != 2 {
		t.Fatalf("after a later commit: %+v %v", v, err)
	}

	outside := *code
	outside.Repo = t.TempDir()
	bad := *code
	bad.Commit = "HEAD; rm -rf /"
	for _, c := range []*ReviewCode{&outside, &bad} {
		r2, err := FileReview(e.home, "x", "", "", "", repo, c, []byte("<p>x</p>"), e.now)
		if err != nil || r2.Code != nil {
			t.Fatalf("kept a bad code reference %+v: %+v %v", c, r2, err)
		}
	}

	plain, _ := FileReview(e.home, "no code", "", "", "", repo, nil, []byte("<p>x</p>"), e.now)
	v, err = e.col.ReviewCodeDiff(plain.ID)
	if err != nil || v.Error == "" || v.Code != nil {
		t.Fatalf("page without code: %+v %v", v, err)
	}
}
