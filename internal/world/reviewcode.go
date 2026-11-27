// internal/world/reviewcode.go
//
// The code a review page is about. `aiagent review --repo DIR` records the
// checkout, its branch, the commit the page describes and the commit that work
// started from. The review view reads the diff between the two from that
// checkout, so a reviewer sees the real change next to the agent's own
// summary of it. Nothing here writes to the checkout.
package world

import (
	"errors"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/hotkhwan/aiagent/internal/orchestrator"
	"github.com/hotkhwan/aiagent/internal/planner"
)

// ReviewCode is the commit a review page describes.
type ReviewCode struct {
	Repo    string `json:"repo"`
	Branch  string `json:"branch,omitempty"`
	Commit  string `json:"commit"`
	Base    string `json:"base"`
	BaseRef string `json:"baseRef,omitempty"`
}

// reviewBaseRefs are tried in order for the commit the work started from:
// the integration branch first, as the team's flow is feat/xxx -> develop.
var reviewBaseRefs = []string{"origin/develop", "develop", "origin/HEAD", "origin/main", "main", "origin/master", "master"}

var branchNameRE = regexp.MustCompile(`^[A-Za-z0-9._/-]{1,200}$`)

func validBranch(b string) bool {
	return branchNameRE.MatchString(b) && !strings.HasPrefix(b, "-") && !strings.Contains(b, "..")
}

func (g RepoGit) revCommit(dir, ref string) (string, bool) {
	out, err := g.read(dir, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
	sha := strings.TrimSpace(string(out))
	return sha, err == nil && shaRE.MatchString(sha)
}

// DetectReviewCode reads the checkout dir is in: its top folder, branch, HEAD
// and the merge base with the first mainline ref that HEAD is not already on.
// A checkout with no such ref falls back to HEAD's first parent.
func DetectReviewCode(g RepoGit, dir string) (*ReviewCode, error) {
	out, err := g.read(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, errors.New("not a git checkout: " + trimErr(string(out)))
	}
	top := filepath.Clean(strings.TrimSpace(string(out)))
	head, ok := g.revCommit(top, "HEAD")
	if !ok {
		return nil, errors.New("the checkout has no commit yet")
	}
	code := &ReviewCode{Repo: top, Commit: head}
	if out, err := g.read(top, "symbolic-ref", "--quiet", "--short", "HEAD"); err == nil {
		if b := strings.TrimSpace(string(out)); validBranch(b) {
			code.Branch = b
		}
	}
	for _, ref := range reviewBaseRefs {
		if _, ok := g.revCommit(top, ref); !ok {
			continue
		}
		mb, err := g.read(top, "merge-base", "HEAD", ref)
		base := strings.TrimSpace(string(mb))
		if err != nil || !shaRE.MatchString(base) || base == head {
			continue // unrelated, or HEAD is on that ref already
		}
		code.Base, code.BaseRef = base, ref
		return code, nil
	}
	if parent, ok := g.revCommit(top, "HEAD^"); ok {
		code.Base, code.BaseRef = parent, "HEAD^"
		return code, nil
	}
	return nil, errors.New("no commit to compare HEAD with")
}

// insideHome reports whether dir is home or a folder under it. Both are
// compared by their real paths: git reports a checkout that way, and a
// symlink inside home must not lead the check out of it.
func insideHome(home, dir string) bool {
	if home == "" || !filepath.IsAbs(dir) {
		return false
	}
	home, dir = realPath(home), realPath(filepath.Clean(dir))
	rel, err := filepath.Rel(home, dir)
	return err == nil && (rel == "." || filepath.IsLocal(rel))
}

// realPath resolves symlinks in p, or returns p as given when it does not exist.
func realPath(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}

// cleanReviewCode keeps a recorded code reference only when it names a
// checkout inside home and real-looking commits.
func cleanReviewCode(home string, c *ReviewCode) *ReviewCode {
	if c == nil || !insideHome(home, c.Repo) || !shaRE.MatchString(c.Commit) || !shaRE.MatchString(c.Base) {
		return nil
	}
	out := &ReviewCode{Repo: filepath.Clean(c.Repo), Commit: c.Commit, Base: c.Base}
	if validBranch(c.Branch) {
		out.Branch = c.Branch
	}
	if c.BaseRef == "HEAD^" || validBranch(c.BaseRef) {
		out.BaseRef = c.BaseRef
	}
	return out
}

// reviewCodeView is what the review's code tab shows.
type reviewCodeView struct {
	Code          *ReviewCode        `json:"code,omitempty"`
	Files         []planner.FileStat `json:"files"`
	Diff          string             `json:"diff"`
	DiffTruncated bool               `json:"diffTruncated"`
	// BranchHead is the branch's commit now, when it has moved on since
	// the page was filed: the page describes Commit, not the latest work.
	BranchHead string `json:"branchHead,omitempty"`
	Error      string `json:"error,omitempty"`
}

// ReviewCodeDiff reads the change a review page describes.
func (c *Collector) ReviewCodeDiff(id string) (reviewCodeView, error) {
	v := reviewCodeView{Files: []planner.FileStat{}}
	r, err := c.review(id)
	if err != nil {
		return v, err
	}
	code := cleanReviewCode(r.home, r.Code)
	if code == nil {
		v.Error = "หน้านี้ไม่ได้ผูกกับ commit: agent ส่งหน้าโดยไม่มี --repo"
		return v, nil
	}
	v.Code = code
	g := RepoGit{R: c.Runner}
	files, err := g.DiffStat(code.Repo, code.Base, code.Commit)
	if err != nil {
		v.Error = err.Error()
		return v, nil
	}
	for i := range files {
		files[i].Protected = orchestrator.IsProtected(files[i].Path)
	}
	v.Files = files
	if v.Diff, v.DiffTruncated, err = g.Diff(code.Repo, code.Base, code.Commit); err != nil {
		v.Error = err.Error()
	}
	if code.Branch != "" {
		if now, ok := g.revCommit(code.Repo, "refs/heads/"+code.Branch); ok && now != code.Commit {
			v.BranchHead = now
		}
	}
	return v, nil
}
