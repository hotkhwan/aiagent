// Package orchestrator moves tasks along: it readies the ones whose
// dependencies are done, dispatches a ready task into an agent's session when
// the user approves, and turns what the agent reports into the next step.
//
// It is the deterministic half of the PM. It decides nothing a model could
// talk it into: every move goes through the state machine in taskstore, every
// dispatch is recorded before it is typed, and what an agent writes back is
// checked against git before it counts. The agents and git are behind two
// small interfaces, so all of this is tested without tmux or a repository.
package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/hotkhwan/aiagent/internal/taskstore"
)

// Workspace is an agent's place: its name, the CLI in its window and the
// checkout it works in.
type Workspace struct {
	Name     string
	Provider string
	Dir      string
	// TestCommand is the operator's argv for verifying work in this
	// workspace. It comes from the service's config, never the repository.
	TestCommand []string
}

// Delivery is what is known after typing into a session: which session it
// was, and whether its transcript shows the text arrived.
type Delivery struct {
	SessionID string
	Acked     bool
}

// Agents is the sessions of claude, codex and gemini.
type Agents interface {
	// Pick chooses a workspace whose agent runs provider ("" or "auto" lets
	// routing choose).
	Pick(ctx context.Context, provider string) (Workspace, error)
	// Send types text into the workspace's agent.
	Send(ctx context.Context, ws Workspace, text string) (Delivery, error)
	// Lookup finds a configured workspace by name.
	Lookup(name string) (Workspace, bool)
	// LastActivity is when a session's transcript last moved.
	LastActivity(ctx context.Context, sessionID string) (time.Time, bool)
}

// HeadlessWorkspace names the workspace of an attempt run by the headless
// worker, which has no terminal session.
const HeadlessWorkspace = "headless"

// ErrWorkerMissing means the worker CLI is not installed. The task is blocked
// with missingCli, not failed, so it can run once the CLI is there.
var ErrWorkerMissing = errors.New("worker CLI not installed")

// Headless runs an attempt as a worker process in a checkout of its own.
type Headless interface {
	// Workspace is the repository the workers branch from.
	Workspace() Workspace
	// Start begins the attempt's worker at base with prompt. It returns
	// ErrWorkerMissing when no CLI can run.
	Start(ctx context.Context, a taskstore.Attempt, base, prompt string) error
	// Running reports whether the attempt's worker is still going.
	Running(attemptID string) bool
	// Outcome is the attempt's result once its worker has stopped.
	Outcome(attemptID string) (Result, bool)
}

// Git answers what the orchestrator asks of a checkout.
type Git interface {
	Head(dir string) (string, error)
	HasCommit(dir, sha string) bool
	// ChangedFiles lists the paths that differ between two commits.
	ChangedFiles(dir, base, commit string) ([]string, error)
	// RunTests runs argv at commit, in a checkout of its own so the
	// operator's working tree is not touched.
	RunTests(ctx context.Context, dir, commit string, argv []string) (TestRun, error)
}

// TestRun is how a test command came out.
type TestRun struct {
	Passed bool
	Output string // the tail, for the reason or the evidence
}

// Budget says whether a provider may take new work right now.
type Budget interface {
	Admit(ctx context.Context, provider string) error
}

// Orchestrator is the control loop over the task store.
type Orchestrator struct {
	Store  *taskstore.Store
	Agents Agents
	Git    Git
	// Headless, when set, runs dispatched tasks as worker processes instead of
	// typing them into Agents.
	Headless Headless
	// Budget gates dispatch; nil admits everything.
	Budget Budget
	// Lease is how long an attempt may go without activity before it is
	// reported as expired.
	Lease time.Duration
	Now   func() time.Time

	mu sync.Mutex // one dispatch or tick at a time
}

func (o *Orchestrator) now() time.Time {
	if o.Now != nil {
		return o.Now()
	}
	return time.Now()
}

// Promote readies queued tasks whose dependencies are all completed, and
// blocks — naming the dependency — those waiting on one that was cancelled or
// failed. It returns how many tasks it moved.
func (o *Orchestrator) Promote() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	moved := 0
	for _, t := range o.Store.List() {
		if t.Status != taskstore.StatusQueued {
			continue
		}
		ready, why := true, ""
		for _, dep := range t.BlockedBy {
			d, ok := o.Store.Get(dep)
			switch {
			case !ok:
				why = "dependencyMissing: " + dep
			case d.Status == taskstore.StatusCancelled || d.Status == taskstore.StatusFailed:
				why = "dependencyFailed: " + dep + " is " + string(d.Status)
			case d.Status != taskstore.StatusCompleted:
				ready = false
			}
			if why != "" {
				break
			}
		}
		var err error
		switch {
		case why != "":
			_, err = o.Store.Transition(t.ID, taskstore.StatusBlocked, taskstore.ActorOrchestrator, taskstore.Change{Reason: why}, "")
		case ready:
			_, err = o.Store.Transition(t.ID, taskstore.StatusReady, taskstore.ActorOrchestrator, taskstore.Change{}, "")
		default:
			continue
		}
		if err == nil {
			moved++
		}
	}
	return moved
}

// Dispatch sends a ready task to an agent. The user has approved it; the
// orchestrator picks the workspace, records the attempt, and only then types.
// A send that failed outright ends the attempt and requeues the task; one that
// was typed but did not show up in the transcript is left unknown, holding the
// task, and is never resent by itself.
func (o *Orchestrator) Dispatch(ctx context.Context, taskID string) (taskstore.Attempt, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	t, ok := o.Store.Get(taskID)
	if !ok {
		return taskstore.Attempt{}, fmt.Errorf("no task %s", taskID)
	}
	if t.Status != taskstore.StatusReady {
		return taskstore.Attempt{}, fmt.Errorf("งาน %s เป็น %s ส่งให้ agent ได้เฉพาะงานที่อนุมัติแล้ว", t.ID, t.Status)
	}
	provider := t.Provider
	if provider == "" {
		provider = "auto"
	}
	if o.Budget != nil {
		if err := o.Budget.Admit(ctx, provider); err != nil {
			return taskstore.Attempt{}, err
		}
	}
	var ws Workspace
	if o.Headless != nil {
		ws = o.Headless.Workspace()
	} else {
		var err error
		if ws, err = o.Agents.Pick(ctx, provider); err != nil {
			return taskstore.Attempt{}, err
		}
	}
	base, err := o.Git.Head(ws.Dir)
	if err != nil {
		return taskstore.Attempt{}, fmt.Errorf("workspace %s: no base commit: %w", ws.Name, err)
	}
	attemptID := fmt.Sprintf("%s-A%d", t.ID, len(t.Attempts)+1)
	branch := "aiagent/" + t.ID + "/" + attemptID
	_, a, err := o.Store.StartAttempt(t.ID, taskstore.AttemptStart{Provider: ws.Provider, Workspace: ws.Name, BaseCommit: base, Branch: branch}, o.Lease, "")
	if err != nil {
		return taskstore.Attempt{}, err
	}
	if a.ID != attemptID {
		return a, fmt.Errorf("attempt id %s, expected %s", a.ID, attemptID)
	}

	if o.Headless != nil {
		return o.startHeadless(ctx, t, a, base)
	}
	d, err := o.Agents.Send(ctx, ws, Envelope(t, a))
	if err != nil {
		reason := "dispatch failed: " + err.Error()
		if _, e := o.Store.EndAttempt(a.ID, taskstore.AttemptFailed, reason, ""); e == nil {
			_, _ = o.Store.Transition(t.ID, taskstore.StatusQueued, taskstore.ActorOrchestrator, taskstore.Change{Reason: reason}, "")
		}
		return a, err
	}
	if d.Acked {
		return o.Store.MarkRunning(a.ID, d.SessionID)
	}
	return o.Store.MarkUnknown(a.ID)
}

func (o *Orchestrator) startHeadless(ctx context.Context, t taskstore.Task, a taskstore.Attempt, base string) (taskstore.Attempt, error) {
	err := o.Headless.Start(ctx, a, base, HeadlessEnvelope(t, a))
	if err == nil {
		return o.Store.MarkRunning(a.ID, "")
	}
	reason, target := "dispatch failed: "+err.Error(), taskstore.StatusQueued
	if errors.Is(err, ErrWorkerMissing) {
		reason, target = "missingCli: "+err.Error(), taskstore.StatusBlocked
	}
	if _, e := o.Store.EndAttempt(a.ID, taskstore.AttemptFailed, reason, ""); e == nil {
		_, _ = o.Store.Transition(t.ID, taskstore.StatusQueued, taskstore.ActorOrchestrator, taskstore.Change{Reason: reason}, "")
		if target == taskstore.StatusBlocked {
			_, _ = o.Store.Transition(t.ID, target, taskstore.ActorOrchestrator, taskstore.Change{Reason: reason}, "")
		}
	}
	return a, err
}

// ConfirmDelivery records that the agent has the work after all — the user
// saw it in the session — so an unknown attempt runs.
func (o *Orchestrator) ConfirmDelivery(attemptID, sessionID string) (taskstore.Attempt, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.Store.MarkRunning(attemptID, sessionID)
}

// Tick renews leases on transcript activity and acts on result files.
func (o *Orchestrator) Tick(ctx context.Context) {
	o.mu.Lock()
	defer o.mu.Unlock()
	for _, t := range o.Store.List() {
		if t.Status != taskstore.StatusRunning {
			continue
		}
		var a *taskstore.Attempt
		for i := range t.Attempts {
			switch t.Attempts[i].Status {
			case taskstore.AttemptStarting, taskstore.AttemptRunning, taskstore.AttemptUnknown:
				a = &t.Attempts[i]
			}
		}
		if a == nil {
			continue
		}
		if o.Headless != nil && a.Workspace == HeadlessWorkspace {
			o.tickHeadless(t, *a)
			continue
		}
		if ws, ok := o.Agents.Lookup(a.Workspace); ok {
			if o.applyResult(t, *a, ws) {
				continue
			}
		}
		if a.SessionID != "" {
			if at, ok := o.Agents.LastActivity(ctx, a.SessionID); ok && at.After(a.HeartbeatAt) {
				_, _ = o.Store.Heartbeat(a.ID)
			}
		}
	}
}

func (o *Orchestrator) tickHeadless(t taskstore.Task, a taskstore.Attempt) {
	if r, done := o.Headless.Outcome(a.ID); done {
		o.applyOutcome(t, a, r, o.Headless.Workspace().Dir)
		return
	}
	if o.Headless.Running(a.ID) {
		_, _ = o.Store.Heartbeat(a.ID)
	}
}

func (o *Orchestrator) lookup(name string) (Workspace, bool) {
	if o.Headless != nil && name == HeadlessWorkspace {
		return o.Headless.Workspace(), true
	}
	return o.Agents.Lookup(name)
}

// Result is what an agent writes to .aiagent/results/<attemptId>.json.
type Result struct {
	Status  string `json:"status"` // done | blocked
	Commit  string `json:"commit,omitempty"`
	Summary string `json:"summary,omitempty"`
}

// ResultPath is where the agent of attempt a reports, inside its checkout.
func ResultPath(dir, attemptID string) string {
	return filepath.Join(dir, ".aiagent", "results", attemptID+".json")
}

var commitRE = regexp.MustCompile(`^[0-9a-f]{7,40}$`)

const maxResultBytes = 64 << 10

// applyResult reads an attempt's result file and moves the task on. The file
// is written by the agent in the repository, so it is untrusted: it is size
// capped, only its three fields are read, and a "done" counts only when the
// commit it names exists. It reports whether it moved the task.
func (o *Orchestrator) applyResult(t taskstore.Task, a taskstore.Attempt, ws Workspace) bool {
	r, err := readResult(ResultPath(ws.Dir, a.ID))
	if err != nil {
		return false
	}
	return o.applyOutcome(t, a, r, ws.Dir)
}

// LoadResult reads the result file an attempt left in its checkout at dir.
func LoadResult(dir, attemptID string) (Result, error) {
	return readResult(ResultPath(dir, attemptID))
}

// applyOutcome moves a task on from what its attempt reported. The commit a
// "done" names is looked up in the repository at dir.
func (o *Orchestrator) applyOutcome(t taskstore.Task, a taskstore.Attempt, r Result, dir string) bool {
	summary := clip(r.Summary, 500)
	switch r.Status {
	case "done":
		if !commitRE.MatchString(r.Commit) || !o.Git.HasCommit(dir, r.Commit) {
			return false
		}
		if _, err := o.Store.ReportAttempt(a.ID, r.Commit, summary); err != nil {
			return false
		}
		_, err := o.Store.Transition(t.ID, taskstore.StatusVerifying, taskstore.ActorOrchestrator,
			taskstore.Change{Evidence: map[string]string{"commit": r.Commit}}, "")
		return err == nil
	case "blocked":
		reason := "agent blocked: " + summary
		if _, err := o.Store.EndAttempt(a.ID, taskstore.AttemptFailed, reason, ""); err != nil {
			return false
		}
		_, err := o.Store.Transition(t.ID, taskstore.StatusFailed, taskstore.ActorOrchestrator, taskstore.Change{Reason: reason}, "")
		return err == nil
	}
	return false
}

func readResult(path string) (Result, error) {
	f, err := os.Open(path)
	if err != nil {
		return Result{}, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxResultBytes+1))
	if err != nil {
		return Result{}, err
	}
	if len(b) > maxResultBytes {
		return Result{}, errors.New("result file too large")
	}
	var r Result
	if err := json.Unmarshal(b, &r); err != nil {
		return Result{}, err
	}
	return r, nil
}

func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

// CheckBase runs before a reviewed task can complete. If the workspace's base
// has moved since the attempt started, the reviewed work is sent back to
// verifying, so its tests run again before anyone can complete it. It reports
// whether it did.
func (o *Orchestrator) CheckBase(ctx context.Context, taskID string) (bool, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	t, ok := o.Store.Get(taskID)
	if !ok {
		return false, fmt.Errorf("no task %s", taskID)
	}
	if t.Status != taskstore.StatusAwaitingReview || len(t.Attempts) == 0 {
		return false, nil
	}
	a := t.Attempts[len(t.Attempts)-1]
	ws, ok := o.lookup(a.Workspace)
	if !ok {
		return false, fmt.Errorf("workspace %s is no longer configured", a.Workspace)
	}
	head, err := o.Git.Head(ws.Dir)
	if err != nil {
		return false, fmt.Errorf("workspace %s: %w", ws.Name, err)
	}
	if head == a.BaseCommit {
		return false, nil
	}
	reason := fmt.Sprintf("base moved from %s to %s since review; tests run again", a.BaseCommit, head)
	if _, err := o.Store.Transition(t.ID, taskstore.StatusVerifying, taskstore.ActorOrchestrator, taskstore.Change{Reason: reason}, ""); err != nil {
		return false, err
	}
	return true, nil
}
