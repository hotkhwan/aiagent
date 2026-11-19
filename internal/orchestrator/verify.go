package orchestrator

import (
	"context"
	"path"
	"strings"
	"sync"

	"github.com/hotkhwan/aiagent/internal/taskstore"
)

// verifying keeps verification one run at a time without holding the
// orchestrator's lock, so dispatch stays responsive while tests run.
var verifying sync.Mutex

// protected are paths through which work could make itself pass — tests, CI,
// lint and module files, agent configuration. Touching them is sometimes the
// task, so it is not refused; it is flagged for the user's review.
var protected = []string{
	".github/", ".claude/", ".mcp.json", ".codex/", ".gemini/",
	"CLAUDE.md", "AGENTS.md", "GEMINI.md",
	"go.mod", "go.sum", "vendor/", "testdata/",
	"package.json", "package-lock.json", ".golangci",
}

// IsProtected reports whether p is one of those paths. The review page marks
// such files so the user looks at them first.
func IsProtected(p string) bool {
	if strings.HasSuffix(p, "_test.go") || strings.Contains(p, ".test.") || strings.Contains(p, ".spec.") {
		return true
	}
	for _, x := range protected {
		if strings.HasSuffix(x, "/") && (strings.HasPrefix(p, x) || strings.Contains(p, "/"+x)) {
			return true
		}
		if p == x || strings.HasSuffix(p, "/"+x) || strings.HasPrefix(path.Base(p), x) && strings.HasPrefix(x, ".") {
			return true
		}
	}
	return false
}

// allowed reports whether p is inside one of the task's allowed paths. A
// pattern ending in /** covers everything below it; anything else is a
// path.Match pattern. No patterns means the whole repository.
func allowed(p string, patterns []string) bool {
	if len(patterns) == 0 {
		return true
	}
	for _, pat := range patterns {
		if dir, ok := strings.CutSuffix(pat, "/**"); ok {
			if p == dir || strings.HasPrefix(p, dir+"/") {
				return true
			}
			continue
		}
		if ok, _ := path.Match(pat, p); ok {
			return true
		}
	}
	return false
}

// Verify checks every task in verifying: the files its commit changed must be
// inside the task's allowed paths, and the workspace's own test command must
// pass at that commit. Passing work waits for the user with the evidence;
// failing work goes back to the queue with the reason, or fails once its
// attempts are used up.
func (o *Orchestrator) Verify(ctx context.Context) {
	if !verifying.TryLock() {
		return
	}
	defer verifying.Unlock()
	for _, t := range o.Store.List() {
		if t.Status != taskstore.StatusVerifying {
			continue
		}
		o.verifyOne(ctx, t)
	}
}

func (o *Orchestrator) verifyOne(ctx context.Context, t taskstore.Task) {
	var a *taskstore.Attempt
	for i := range t.Attempts {
		if t.Attempts[i].Status == taskstore.AttemptReported && t.Attempts[i].Commit != "" {
			a = &t.Attempts[i]
		}
	}
	if a == nil {
		return
	}
	ws, ok := o.lookup(a.Workspace)
	if !ok {
		return
	}

	files, err := o.Git.ChangedFiles(ws.Dir, a.BaseCommit, a.Commit)
	if err != nil {
		o.sendBack(t, "could not list the changed files: "+clip(err.Error(), 300))
		return
	}
	var outside, flagged []string
	for _, f := range files {
		if !allowed(f, t.AllowedPaths) {
			outside = append(outside, f)
		}
		if IsProtected(f) {
			flagged = append(flagged, f)
		}
	}
	if len(outside) > 0 {
		o.sendBack(t, "changed files outside the allowed paths: "+clip(strings.Join(outside, ", "), 500))
		return
	}

	evidence := map[string]string{
		"commit":       a.Commit,
		"changedFiles": clip(strings.Join(files, ", "), 1000),
	}
	if len(flagged) > 0 {
		evidence["needsHumanReview"] = clip(strings.Join(flagged, ", "), 500)
	}
	if len(ws.TestCommand) == 0 {
		evidence["tests"] = "notRun: no testCommand is configured for workspace " + ws.Name
	} else {
		run, err := o.Git.RunTests(ctx, ws.Dir, a.Commit, ws.TestCommand)
		cmd := strings.Join(ws.TestCommand, " ")
		switch {
		case err != nil:
			o.sendBack(t, "tests could not run ("+cmd+"): "+clip(err.Error(), 300))
			return
		case !run.Passed:
			o.sendBack(t, "tests failed ("+cmd+"): "+clip(run.Output, 800))
			return
		}
		evidence["tests"] = "passed: " + cmd
	}
	_, _ = o.Store.Transition(t.ID, taskstore.StatusAwaitingReview, taskstore.ActorOrchestrator, taskstore.Change{Evidence: evidence}, "")
}

// sendBack requeues work that did not pass, or fails it once every attempt
// it was allowed has been used.
func (o *Orchestrator) sendBack(t taskstore.Task, reason string) {
	to := taskstore.StatusQueued
	if len(t.Attempts) >= t.MaxAttempts {
		to = taskstore.StatusFailed
		reason += " — no attempts left"
	}
	_, _ = o.Store.Transition(t.ID, to, taskstore.ActorOrchestrator, taskstore.Change{Reason: reason}, "")
}
