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
	return &orchestrator.Orchestrator{Store: ts, Agents: AgentSessions{C: c}, Git: RepoGit{R: c.Runner}, Lease: lease}
}

// AgentSessions implements orchestrator.Agents over the collector.
type AgentSessions struct{ C *Collector }

func asWorkspace(w workspaceEntry) orchestrator.Workspace {
	return orchestrator.Workspace{Name: w.name, Provider: w.provider, Dir: w.dir}
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
				}
			}
		}
	}()
}
