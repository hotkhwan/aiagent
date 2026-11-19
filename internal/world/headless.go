// internal/world/headless.go
//
// Runs dispatched attempts as headless claude processes, each in a worktree of
// its own. The worker writes its result into that worktree, and the orchestrator
// reads it back from there. Nothing is typed into a terminal.
package world

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/hotkhwan/aiagent/internal/orchestrator"
	"github.com/hotkhwan/aiagent/internal/sandbox"
	"github.com/hotkhwan/aiagent/internal/taskstore"
	"github.com/hotkhwan/aiagent/internal/worker"
)

// Headless implements orchestrator.Headless over the worker package. Outcomes
// are kept in memory: a restart forgets them, and the attempt's lease then
// expires it.
type Headless struct {
	// Repo is the checkout attempts branch from. Attempt worktrees live under
	// its .worktrees directory.
	Repo string
	// TestCommand verifies an agent's commit in Repo.
	TestCommand []string
	// StateDir is denied to workers: it holds the orchestrator's own logs.
	StateDir string
	// Home is the operator's home directory, whose secret paths the sandbox denies.
	Home string
	// Bin is the claude CLI. Empty means "claude" on PATH.
	Bin string

	mu   sync.Mutex
	runs map[string]*headlessRun
}

type headlessRun struct {
	done    chan struct{}
	outcome orchestrator.Result
}

func (h *Headless) Workspace() orchestrator.Workspace {
	return orchestrator.Workspace{Name: orchestrator.HeadlessWorkspace, Provider: "claude", Dir: h.Repo, TestCommand: h.TestCommand}
}

// Start creates the attempt's worktree at base and runs the worker in the
// background. It returns ErrWorkerMissing before touching git when no CLI is
// installed.
func (h *Headless) Start(ctx context.Context, a taskstore.Attempt, base, prompt string) error {
	bin := h.Bin
	if bin == "" {
		p, err := exec.LookPath("claude")
		if err != nil {
			return orchestrator.ErrWorkerMissing
		}
		bin = p
	}
	common, err := gitCommonDir(ctx, h.Repo)
	if err != nil {
		return err
	}
	tmp, err := os.MkdirTemp("", "aiagent-worker-")
	if err != nil {
		return err
	}
	branch := "aiagent/" + a.ID
	wt := filepath.Join(h.Repo, ".worktrees", a.ID)
	if err := worker.AddWorktree(ctx, h.Repo, base, branch, wt); err != nil {
		os.RemoveAll(tmp)
		return err
	}

	// A linked worktree's commit writes objects, refs, reflogs and its own
	// worktree metadata in the common git directory. Config and hooks stay out:
	// the orchestrator runs git unsandboxed, so a hook written there would run
	// with the operator's rights.
	policy := sandbox.DefaultPolicy(h.Home, h.StateDir, wt, tmp)
	for _, sub := range []string{"objects", "refs", "logs", "worktrees", "packed-refs", "packed-refs.lock"} {
		policy.Writable = append(policy.Writable, filepath.Join(common, sub))
	}

	run := &headlessRun{done: make(chan struct{})}
	h.mu.Lock()
	if h.runs == nil {
		h.runs = map[string]*headlessRun{}
	}
	h.runs[a.ID] = run
	h.mu.Unlock()

	go func() {
		defer close(run.done)
		defer os.RemoveAll(tmp)
		out, err := worker.Claude{Bin: bin, Sandbox: policy, TmpDir: tmp}.Run(context.Background(), wt, prompt, nil)
		run.outcome = h.finish(a.ID, wt, base, out, err)
	}()
	return nil
}

// finish reads the worker's result and removes the worktree once it has
// committed. A worktree without a commit stays, so the operator can see what
// the worker left.
func (h *Headless) finish(id, wt, base string, out worker.Outcome, err error) orchestrator.Result {
	if err != nil {
		return orchestrator.Result{Status: "blocked", Summary: "worker did not start: " + trimErr(err.Error())}
	}
	r, rerr := orchestrator.LoadResult(wt, id)
	if rerr != nil {
		summary := "worker stopped without a result file"
		if out.ExitErr != nil {
			summary += ": " + trimErr(Redact(out.Stderr))
		}
		return orchestrator.Result{Status: "blocked", Summary: summary}
	}
	if r.Status == "done" {
		_ = worker.Cleanup(context.Background(), h.Repo, wt, "aiagent/"+id, base)
	}
	return r
}

// gitCommonDir is the directory that holds a repository's objects and refs. In a
// linked worktree it is not <worktree>/.git, which is only a file there.
func gitCommonDir(ctx context.Context, repo string) (string, error) {
	out, err := exec.CommandContext(ctx, "git", "-C", repo, "rev-parse", "--path-format=absolute", "--git-common-dir").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func (h *Headless) Running(attemptID string) bool {
	run, ok := h.run(attemptID)
	if !ok {
		return false
	}
	select {
	case <-run.done:
		return false
	default:
		return true
	}
}

func (h *Headless) Outcome(attemptID string) (orchestrator.Result, bool) {
	run, ok := h.run(attemptID)
	if !ok {
		return orchestrator.Result{}, false
	}
	select {
	case <-run.done:
		return run.outcome, true
	default:
		return orchestrator.Result{}, false
	}
}

func (h *Headless) run(attemptID string) (*headlessRun, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	run, ok := h.runs[attemptID]
	return run, ok
}
