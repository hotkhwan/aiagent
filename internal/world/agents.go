// internal/world/agents.go
//
// The orchestrator's view of this machine: the agent sessions it dispatches
// into and the checkouts it checks results against. Both go through the same
// guarded paths as everything else — routing that checks what a pane really
// runs, agcmd with a fixed argv, the transcript as proof of delivery, and git
// with a fixed argv.
package world

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"

	"github.com/hotkhwan/aiagent/internal/orchestrator"
	"github.com/hotkhwan/aiagent/internal/taskstore"
)

// NewOrchestrator wires the orchestrator to this collector's sessions and
// checkouts.
func NewOrchestrator(ts *taskstore.Store, c *Collector, lease time.Duration) *orchestrator.Orchestrator {
	return &orchestrator.Orchestrator{Store: ts, Agents: AgentSessions{C: c}, Git: RepoGit{R: c.Runner}, Budget: QuotaBudget{C: c}, Lease: lease}
}

// AgentSessions implements orchestrator.Agents over the collector.
type AgentSessions struct{ C *Collector }

func asWorkspace(w workspaceEntry) orchestrator.Workspace {
	return orchestrator.Workspace{Name: w.name, Provider: w.provider, Dir: w.dir, TestCommand: w.testCommand}
}

// Pick routes by provider, checking the pane runs that provider's CLI.
func (a AgentSessions) Pick(ctx context.Context, provider string) (orchestrator.Workspace, error) {
	w, err := a.C.routeMission(ctx, provider)
	if err != nil {
		return orchestrator.Workspace{}, err
	}
	return asWorkspace(w), nil
}

// Send types text into the workspace's agent pane, then looks for the session
// that pane runs and waits for its transcript to show the text.
func (a AgentSessions) Send(ctx context.Context, ws orchestrator.Workspace, text string) (orchestrator.Delivery, error) {
	sentAt := a.C.now()
	if err := a.C.Message(ctx, ws.Name, text); err != nil {
		return orchestrator.Delivery{}, err
	}
	a.C.invalidate()
	for _, s := range a.C.Snapshot(ctx).Sessions {
		if s.Live && s.DispatchVia == ws.Name {
			return orchestrator.Delivery{SessionID: s.SessionID, Acked: a.C.AwaitDelivery(ctx, s.SessionID, text, sentAt) == DeliveryAcked}, nil
		}
	}
	// No session is known to run in that pane (a codex or gemini pane on a
	// host where it cannot be traced): the text was typed, receipt unproven.
	return orchestrator.Delivery{}, nil
}

// Lookup finds a configured workspace.
func (a AgentSessions) Lookup(name string) (orchestrator.Workspace, bool) {
	w, ok := a.C.lookupWorkspace(name)
	return asWorkspace(w), ok
}

// LastActivity is the session's last transcript event.
func (a AgentSessions) LastActivity(ctx context.Context, sessionID string) (time.Time, bool) {
	for _, s := range a.C.Snapshot(ctx).Sessions {
		if s.SessionID == sessionID && s.LastEventAt != nil {
			return *s.LastEventAt, true
		}
	}
	return time.Time{}, false
}

// RepoGit implements orchestrator.Git with a fixed argv.
type RepoGit struct{ R Runner }

var (
	shaRE       = regexp.MustCompile(`^[0-9a-f]{7,40}$`)
	sessionIDRE = regexp.MustCompile(`^[A-Za-z0-9-]{0,64}$`)
)

// Head is the checkout's current commit.
func (g RepoGit) Head(dir string) (string, error) {
	out, err := g.R.Run(context.Background(), "git", "-C", dir, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	sha := strings.TrimSpace(string(out))
	if !shaRE.MatchString(sha) {
		return "", errors.New("not a commit: " + trimErr(sha))
	}
	return sha, nil
}

// HasCommit reports whether sha names a commit in the checkout.
func (g RepoGit) HasCommit(dir, sha string) bool {
	if !shaRE.MatchString(sha) {
		return false
	}
	_, err := g.R.Run(context.Background(), "git", "-C", dir, "cat-file", "-e", sha+"^{commit}")
	return err == nil
}

// ChangedFiles lists the paths that differ between base and commit.
func (g RepoGit) ChangedFiles(dir, base, commit string) ([]string, error) {
	if !shaRE.MatchString(base) || !shaRE.MatchString(commit) {
		return nil, errors.New("not a commit")
	}
	out, err := g.R.Run(context.Background(), "git", "-C", dir, "diff", "--name-only", "--no-renames", base, commit, "--")
	if err != nil {
		return nil, errors.New("git diff: " + trimErr(string(out)))
	}
	var files []string
	for _, l := range strings.Split(string(out), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			files = append(files, path.Clean(l))
		}
	}
	return files, nil
}

// testRunner runs a test command in a directory and returns its combined
// output, keeping the tail where failures are reported.
type testRunner interface {
	RunTestIn(ctx context.Context, dir string, argv []string) ([]byte, error)
}

const verifyTimeout = 20 * time.Minute

// RunTests checks the commit out into a worktree of its own, runs argv there
// and removes the worktree, so the operator's checkout is never touched. A
// non-zero exit is a failed run; a command that could not start, or ran out
// of time, is an error.
func (g RepoGit) RunTests(ctx context.Context, dir, commit string, argv []string) (orchestrator.TestRun, error) {
	tr, ok := g.R.(testRunner)
	if !ok || len(argv) == 0 || !shaRE.MatchString(commit) {
		return orchestrator.TestRun{}, errors.New("cannot run tests here")
	}
	tmp, err := os.MkdirTemp("", "aiagent-verify-")
	if err != nil {
		return orchestrator.TestRun{}, err
	}
	defer os.RemoveAll(tmp)
	wt := filepath.Join(tmp, "wt")
	if out, err := g.R.Run(ctx, "git", "-C", dir, "worktree", "add", "--detach", wt, commit); err != nil {
		return orchestrator.TestRun{}, errors.New("git worktree add: " + trimErr(string(out)))
	}
	defer g.R.Run(context.Background(), "git", "-C", dir, "worktree", "remove", "--force", wt)

	ctx, cancel := context.WithTimeout(ctx, verifyTimeout)
	defer cancel()
	out, err := tr.RunTestIn(ctx, wt, argv)
	tail := string(out)
	if len(tail) > 2000 {
		tail = "…" + tail[len(tail)-2000:]
	}
	tail = Redact(tail)
	var exit *exec.ExitError
	switch {
	case ctx.Err() != nil:
		return orchestrator.TestRun{}, errors.New("timed out after " + verifyTimeout.String())
	case errors.As(err, &exit):
		return orchestrator.TestRun{Passed: false, Output: tail}, nil
	case err != nil:
		return orchestrator.TestRun{}, err
	}
	return orchestrator.TestRun{Passed: true, Output: tail}, nil
}

// RunTestIn runs argv in dir with a fixed argv and keeps the last 64 KiB of
// its combined output.
func (ExecRunner) RunTestIn(ctx context.Context, dir string, argv []string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "TERM=dumb")
	out, err := cmd.CombinedOutput()
	if len(out) > 64<<10 {
		out = out[len(out)-64<<10:]
	}
	return out, err
}

func (s *Service) handleTaskDispatch(c fiber.Ctx) error {
	if s.Tasks == nil || s.Orch == nil {
		return hidden(c)
	}
	id := c.Params("id")
	return s.mutate(c, "aiagent.taskDispatch", []string{id}, func() (any, error) {
		if _, ok := s.Tasks.Get(id); !ok {
			return nil, ErrNotFound("no such task")
		}
		a, err := s.Orch.Dispatch(c.Context(), id)
		if err != nil {
			var ae *APIError
			if errors.As(err, &ae) {
				return nil, err
			}
			return nil, ErrConflict(err.Error())
		}
		return a, nil
	})
}

func (s *Service) handleAttemptConfirm(c fiber.Ctx) error {
	if s.Tasks == nil || s.Orch == nil {
		return hidden(c)
	}
	id, aid := c.Params("id"), c.Params("aid")
	var body struct {
		SessionID string `json:"sessionId"`
	}
	_ = c.Bind().Body(&body)
	if !sessionIDRE.MatchString(body.SessionID) {
		return s.fail(c, ErrBadRequest("sessionId must be letters, digits and dashes"))
	}
	return s.mutate(c, "aiagent.attemptConfirm", []string{id, aid}, func() (any, error) {
		t, ok := s.Tasks.Get(id)
		if !ok || !strings.HasPrefix(aid, t.ID+"-A") {
			return nil, ErrNotFound("no such attempt on that task")
		}
		a, err := s.Orch.ConfirmDelivery(aid, body.SessionID)
		if err != nil {
			return nil, ErrConflict(err.Error())
		}
		return a, nil
	})
}

// StartOrchestrator readies and follows tasks while the service is on.
func (s *Service) StartOrchestrator(ctx context.Context) {
	if s.Orch == nil {
		return
	}
	go func() {
		t := time.NewTicker(20 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if s.Store.Enabled() {
					s.Orch.Promote()
					s.Orch.Tick(ctx)
					// Tests can run for minutes; Verify holds no lock that
					// dispatch needs, and skips if a run is already going.
					go s.Orch.Verify(ctx)
				}
			}
		}
	}()
}
