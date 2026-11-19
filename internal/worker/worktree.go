package worker

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
)

var (
	shaRE    = regexp.MustCompile(`^[0-9a-f]{7,40}$`)
	branchRE = regexp.MustCompile(`^aiagent/[A-Za-z0-9._/-]+$`)
)

// AddWorktree checks out base in a new worktree at path on a new branch. Both
// names are checked first so that nothing that reaches git can read as a flag.
func AddWorktree(ctx context.Context, repo, base, branch, path string) error {
	if !shaRE.MatchString(base) {
		return fmt.Errorf("worktree: base %q is not a commit SHA", base)
	}
	if !branchRE.MatchString(branch) {
		return fmt.Errorf("worktree: branch %q is not an aiagent branch", branch)
	}
	_, err := gitIn(ctx, repo, "worktree", "add", "-b", branch, path, base)
	return err
}

// Cleanup removes an attempt's worktree. The branch goes too when it holds no
// commits beyond base; committed work stays on its branch for review.
func Cleanup(ctx context.Context, repo, path, branch, base string) error {
	if _, err := gitIn(ctx, repo, "worktree", "remove", "--force", path); err != nil {
		return err
	}
	if !shaRE.MatchString(base) || !branchRE.MatchString(branch) {
		return errors.New("worktree: cleanup refused for unexpected names")
	}
	ahead, err := gitIn(ctx, repo, "rev-list", "--count", base+".."+branch)
	if err != nil {
		return err
	}
	if strings.TrimSpace(ahead) != "0" {
		return nil
	}
	_, err = gitIn(ctx, repo, "branch", "-D", branch)
	return err
}

func gitIn(ctx context.Context, repo string, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, "git", append([]string{"-C", repo}, args...)...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}
