// internal/world/repodiff.go
//
// What the review page reads from a checkout: how much each file changed, the
// diff itself, and where the work's base commit sits against the mainline.
// Like the rest of RepoGit it is a fixed argv, commits are checked to be
// commit ids before they reach git, and nothing here writes to the checkout.
package world

import (
	"context"
	"errors"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/hotkhwan/aiagent/internal/planner"
)

const (
	gitReadTimeout = 20 * time.Second
	// runnerCap is where ExecRunner cuts output; a diff that long is partial.
	runnerCap = 64 << 10
)

func (g RepoGit) read(dir string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), gitReadTimeout)
	defer cancel()
	return g.R.Run(ctx, "git", append([]string{"-C", dir}, args...)...)
}

// DiffStat lists the files that differ between base and commit with the
// lines added and deleted in each. A binary file has no line counts.
func (g RepoGit) DiffStat(dir, base, commit string) ([]planner.FileStat, error) {
	if !shaRE.MatchString(base) || !shaRE.MatchString(commit) {
		return nil, errors.New("not a commit")
	}
	out, err := g.read(dir, "diff", "--numstat", "-z", "--no-renames", base, commit, "--")
	if err != nil {
		return nil, errors.New("git diff: " + trimErr(string(out)))
	}
	files := []planner.FileStat{}
	for _, rec := range strings.Split(string(out), "\x00") {
		f := strings.SplitN(rec, "\t", 3)
		if len(f) != 3 || f[2] == "" {
			continue
		}
		st := planner.FileStat{Path: path.Clean(f[2])}
		if f[0] == "-" {
			st.Binary = true
		} else {
			st.Added, _ = strconv.Atoi(f[0])
			st.Deleted, _ = strconv.Atoi(f[1])
		}
		files = append(files, st)
	}
	return files, nil
}

// Diff is the textual diff between base and commit, and whether it was cut
// off. External diff drivers and textconv filters are off: they are programs
// the repository's own configuration could name.
func (g RepoGit) Diff(dir, base, commit string) (string, bool, error) {
	if !shaRE.MatchString(base) || !shaRE.MatchString(commit) {
		return "", false, errors.New("not a commit")
	}
	out, err := g.read(dir, "diff", "--no-color", "--no-ext-diff", "--no-textconv", "--no-renames", base, commit, "--")
	if err != nil {
		return "", false, errors.New("git diff: " + trimErr(string(out)))
	}
	return reviewDiffText(out), len(out) >= runnerCap, nil
}

// reviewDiffText makes raw diff bytes safe to serve: valid text, with known
// secret shapes removed. A key that reached a diff should be seen as a
// finding, not copied to every open tab.
func reviewDiffText(out []byte) string {
	return Redact(strings.ToValidUTF8(string(out), "�"))
}

// IsAncestor reports whether commit a is b or one of b's ancestors.
func (g RepoGit) IsAncestor(dir, a, b string) bool {
	if !shaRE.MatchString(a) || !shaRE.MatchString(b) {
		return false
	}
	_, err := g.read(dir, "merge-base", "--is-ancestor", a, b)
	return err == nil
}

// Mainline is the remote's default branch as the checkout last fetched it:
// its short name (origin/main) and commit. ok is false when the checkout has
// no origin/HEAD, which is a thing not known rather than a problem.
func (g RepoGit) Mainline(dir string) (ref, sha string, ok bool) {
	out, err := g.read(dir, "rev-parse", "--verify", "--quiet", "refs/remotes/origin/HEAD^{commit}")
	sha = strings.TrimSpace(string(out))
	if err != nil || !shaRE.MatchString(sha) {
		return "", "", false
	}
	ref = "origin/HEAD"
	if out, err := g.read(dir, "symbolic-ref", "--quiet", "--short", "refs/remotes/origin/HEAD"); err == nil {
		if name := strings.TrimSpace(string(out)); name != "" && len(name) < 200 && !strings.ContainsAny(name, "\n\r") {
			ref = name
		}
	}
	return ref, sha, true
}
