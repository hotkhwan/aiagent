// internal/world/pm.go
//
// The central PM: the user talks to it, it answers with proposed tasks, and
// that is the whole of its reach. It runs as a headless model call with no
// tools, so it cannot touch a checkout or a session; its answer is checked,
// stored, and shown.
//
// Nothing the PM says becomes a task by itself. A proposal turns into a task
// only on POST /pm/proposals/:id/confirm, which acts as the user — the task
// log refuses any other author — and dispatching that task is a second,
// separate confirmation on the task board.
//
//	GET  /aiagent/api/pm                        token — the conversation and what the PM is shown
//	POST /aiagent/api/pm/messages               token — {text} → answered in the background
//	POST /aiagent/api/pm/proposals/:id/confirm  token — optional edits → a queued task
//	POST /aiagent/api/pm/proposals/:id/dismiss  token
//	POST /aiagent/api/pm/reset                  token — start a new conversation
package world

import (
	"context"
	"errors"
	"fmt"
	"log"
	"runtime/debug"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"

	"github.com/hotkhwan/aiagent/internal/planner"
	"github.com/hotkhwan/aiagent/internal/taskstore"
)

// PMAgent is the model behind the PM. planner.Cloud is the real one.
type PMAgent interface {
	Name() string
	Plan(ctx context.Context, in planner.PlanInput) (planner.Plan, planner.Call, error)
	Advise(ctx context.Context, in planner.ReviewInput) (planner.Advice, planner.Call, error)
}

const (
	pmHistoryTurns  = 12
	pmMaxProjects   = 40
	pmMaxTaskBriefs = 60
	pmBriefTitle    = 120
	pmErrorRunes    = 500
	agentStateTTL   = 10 * time.Second
	// pmCrashed is what the user is shown when a PM job panicked. The panic
	// itself goes to the service log.
	pmCrashed = "the PM call crashed inside the service; the service log has the details"
)

func (s *Service) pmOn() bool { return s.PM != nil && s.PMThread != nil && s.Tasks != nil }

func (s *Service) mountPM(api fiber.Router) {
	api.Get("/pm", s.handlePM)
	api.Post("/pm/messages", s.handlePMMessage)
	api.Post("/pm/proposals/:id/confirm", s.handlePMConfirm)
	api.Post("/pm/proposals/:id/dismiss", s.handlePMDismiss)
	api.Post("/pm/reset", s.handlePMReset)
}

// agentStates says, per provider, whether a task could be routed to it now:
// ready, notReady (configured, but its session is not running that CLI) or
// notConfigured (this machine has no workspace for it).
func agentStates(ws []workspaceEntry, ready func(workspaceEntry) bool) []planner.AgentBrief {
	out := make([]planner.AgentBrief, 0, len(routableProviders))
	for _, p := range routableProviders {
		state := "notConfigured"
		for _, w := range ws {
			if w.provider != p || w.protected {
				continue
			}
			if ready(w) {
				state = "ready"
				break
			}
			state = "notReady"
		}
		out = append(out, planner.AgentBrief{Provider: p, State: state})
	}
	return out
}

// pmAgents is agentStates on the live machine. The PM page polls, and each
// answer costs a tmux read, so it is kept for a few seconds.
func (s *Service) pmAgents(ctx context.Context) []planner.AgentBrief {
	s.agentsMu.Lock()
	defer s.agentsMu.Unlock()
	if s.agents == nil || time.Since(s.agentsAt) > agentStateTTL {
		s.agents = agentStates(s.Collector.Workspaces, s.Collector.workspaceReady(ctx))
		s.agentsAt = time.Now()
	}
	return slices.Clone(s.agents)
}

// pmProjects are the names a proposal may file a task under: every configured
// workspace and every project a task already uses.
func (s *Service) pmProjects(tasks []taskstore.Task) []string {
	seen := map[string]bool{}
	out := []string{}
	add := func(name string) {
		if name = strings.TrimSpace(name); name != "" && !seen[name] && len(out) < pmMaxProjects {
			seen[name] = true
			out = append(out, name)
		}
	}
	for _, w := range s.Collector.Workspaces {
		add(w.name)
	}
	for _, t := range tasks {
		add(t.ProjectID)
	}
	sort.Strings(out)
	return out
}

func taskOpen(st taskstore.Status) bool {
	switch st {
	case taskstore.StatusCompleted, taskstore.StatusCancelled, taskstore.StatusFailed:
		return false
	}
	return true
}

// pmTaskBriefs are the tasks still in play, newest last, as the PM sees them.
func pmTaskBriefs(tasks []taskstore.Task) []planner.TaskBrief {
	out := []planner.TaskBrief{}
	for _, t := range tasks {
		if taskOpen(t.Status) {
			out = append(out, planner.TaskBrief{
				ID: t.ID, Title: truncateRunes(t.Title, pmBriefTitle), Status: string(t.Status),
				ProjectID: t.ProjectID, Provider: t.Provider,
			})
		}
	}
	if n := len(out); n > pmMaxTaskBriefs {
		out = out[n-pmMaxTaskBriefs:]
	}
	return out
}

func (s *Service) handlePM(c fiber.Ctx) error {
	if !s.pmOn() {
		return hidden(c)
	}
	v := s.PMThread.View()
	return c.JSON(okEnv(fiber.Map{
		"adapter":       s.PM.Name(),
		"thinking":      v.Thinking,
		"thinkingSince": v.ThinkingSince,
		"messages":      v.Messages,
		"context": fiber.Map{
			"projects": s.pmProjects(s.Tasks.List()),
			"agents":   s.pmAgents(c.Context()),
		},
	}))
}

func (s *Service) handlePMMessage(c fiber.Ctx) error {
	if !s.pmOn() {
		return hidden(c)
	}
	var body struct {
		Text string `json:"text"`
	}
	if err := c.Bind().Body(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(failEnv("AIAGENT_BAD_REQUEST", "invalid JSON body"))
	}
	text, err := cleanProse(body.Text)
	if err != nil {
		return s.fail(c, err)
	}
	// The text is stored, shown in every open tab and sent to a model: a
	// pasted secret stops here.
	text = Redact(text)
	return s.mutate(c, "aiagent.pmMessage", []string{text}, func() (any, error) {
		history := s.PMThread.History(pmHistoryTurns)
		id, err := s.PMThread.Ask(text)
		if errors.Is(err, planner.ErrBusy) {
			return nil, ErrConflict("the PM is still answering the last message")
		}
		if err != nil {
			return nil, err
		}
		tasks := s.Tasks.List()
		in := planner.PlanInput{
			Request:  text,
			History:  history,
			Projects: s.pmProjects(tasks),
			Tasks:    pmTaskBriefs(tasks),
			Agents:   s.pmAgents(c.Context()),
		}
		s.background(func() { s.runPlan(in) }, func() {
			_ = s.PMThread.Fail(pmCrashed, planner.Call{Outcome: "failed"})
		})
		return fiber.Map{"messageId": id, "thinking": true}, nil
	})
}

// background runs one PM job behind a response. Nothing above that goroutine
// would catch a panic, and an uncaught one ends the whole service, so a crash
// in the job is logged and becomes that job's failure through crashed.
func (s *Service) background(job, crashed func()) {
	s.bg.Add(1)
	go func() {
		defer s.bg.Done()
		defer func() {
			if r := recover(); r != nil {
				log.Printf("PM job crashed: %v\n%s", r, debug.Stack())
				crashed()
			}
		}()
		job()
	}()
}

// runPlan asks the model and records what came back. A call takes longer than
// a request should, so it runs behind the response and the page polls.
func (s *Service) runPlan(in planner.PlanInput) {
	ctx, cancel := context.WithTimeout(context.Background(), pmCallTimeout)
	defer cancel()
	plan, call, err := s.PM.Plan(ctx, in)
	if err != nil {
		_ = s.PMThread.Fail(truncateRunes(Redact(err.Error()), pmErrorRunes), call)
		return
	}
	redactPlan(&plan)
	_ = s.PMThread.Reply(plan, call)
}

func redactAll(xs []string) {
	for i := range xs {
		xs[i] = Redact(xs[i])
	}
}

func redactPlan(p *planner.Plan) {
	p.Reply = Redact(p.Reply)
	redactAll(p.Questions)
	for i := range p.Proposals {
		pr := &p.Proposals[i]
		pr.Title, pr.ProviderReason = Redact(pr.Title), Redact(pr.ProviderReason)
		redactAll(pr.AcceptanceCriteria)
	}
}

// proposalEdits are the changes the user made to a proposal before confirming
// it. A field left out keeps what the PM wrote.
type proposalEdits struct {
	Title              *string  `json:"title"`
	ProjectID          *string  `json:"projectId"`
	Kind               *string  `json:"kind"`
	Provider           *string  `json:"provider"`
	AcceptanceCriteria []string `json:"acceptanceCriteria"`
	AllowedPaths       []string `json:"allowedPaths"`
}

// taskFromProposal is the task a proposal becomes: the PM's fields, the
// user's edits over them, and the same checks either way.
func taskFromProposal(p planner.Proposal, e proposalEdits) (taskstore.NewTask, error) {
	nt := taskstore.NewTask{
		ProjectID: p.ProjectID, Title: p.Title, Kind: p.Kind, Provider: p.Provider,
		AcceptanceCriteria: slices.Clone(p.AcceptanceCriteria),
		AllowedPaths:       slices.Clone(p.AllowedPaths),
		RiskLabels:         slices.Clone(p.RiskLabels),
		BlockedBy:          slices.Clone(p.BlockedBy),
	}
	if e.Title != nil {
		nt.Title = strings.TrimSpace(*e.Title)
	}
	if e.ProjectID != nil {
		nt.ProjectID = strings.TrimSpace(*e.ProjectID)
	}
	if e.Kind != nil {
		nt.Kind = strings.TrimSpace(*e.Kind)
	}
	if e.Provider != nil {
		nt.Provider = strings.TrimSpace(*e.Provider)
		if nt.Provider == "auto" {
			nt.Provider = ""
		}
	}
	if e.AcceptanceCriteria != nil {
		nt.AcceptanceCriteria = e.AcceptanceCriteria
	}
	if e.AllowedPaths != nil {
		nt.AllowedPaths = e.AllowedPaths
	}
	if err := checkTaskText(nt); err != nil {
		return nt, err
	}
	if strings.ContainsAny(nt.Title, "\r\n") {
		return nt, ErrBadRequest("title must be one line")
	}
	if !slices.Contains(planner.Kinds, nt.Kind) {
		return nt, ErrBadRequest("kind must be one of " + strings.Join(planner.Kinds, ", "))
	}
	for i, ap := range nt.AllowedPaths {
		nt.AllowedPaths[i] = strings.TrimSpace(ap)
		if err := planner.CheckPath(nt.AllowedPaths[i]); err != nil {
			return nt, ErrBadRequest(err.Error())
		}
	}
	return nt, nil
}

func (s *Service) handlePMConfirm(c fiber.Ctx) error {
	if !s.pmOn() {
		return hidden(c)
	}
	id := c.Params("id")
	var edits proposalEdits
	if len(c.Body()) > 0 {
		if err := c.Bind().Body(&edits); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(failEnv("AIAGENT_BAD_REQUEST", "invalid JSON body"))
		}
	}
	return s.mutate(c, "aiagent.pmConfirm", []string{id}, func() (any, error) {
		p, siblings, ok := s.PMThread.Proposal(id)
		if !ok {
			return nil, ErrNotFound("no such proposal")
		}
		if p.State == "confirmed" {
			if t, ok := s.Tasks.Get(p.TaskID); ok {
				return fiber.Map{"task": t}, nil
			}
		}
		if p.State != "open" {
			return nil, ErrConflict("this proposal was already " + p.State)
		}
		nt, err := taskFromProposal(p.Proposal, edits)
		if err != nil {
			return nil, err
		}
		// A step that waits on an earlier step of the same plan waits on the
		// task that step became. One the user turned down is no longer waited on.
		for _, d := range p.DependsOn {
			if d < 1 || d > len(siblings) {
				continue
			}
			switch dep := siblings[d-1]; dep.State {
			case "confirmed":
				if !slices.Contains(nt.BlockedBy, dep.TaskID) {
					nt.BlockedBy = append(nt.BlockedBy, dep.TaskID)
				}
			case "open":
				return nil, ErrConflict(fmt.Sprintf("this step waits on step %d of the same plan (%s): confirm or dismiss that one first", d, dep.Title))
			}
		}
		key, _ := s.PMThread.ConfirmKey(id)
		t, err := s.Tasks.Create(nt, taskstore.ActorUser, key)
		if err != nil {
			return nil, ErrBadRequest(err.Error())
		}
		if err := s.PMThread.Confirm(id, t.ID); err != nil {
			return nil, err
		}
		return fiber.Map{"task": t}, nil
	})
}

func (s *Service) handlePMDismiss(c fiber.Ctx) error {
	if !s.pmOn() {
		return hidden(c)
	}
	id := c.Params("id")
	return s.mutate(c, "aiagent.pmDismiss", []string{id}, func() (any, error) {
		p, _, ok := s.PMThread.Proposal(id)
		if !ok {
			return nil, ErrNotFound("no such proposal")
		}
		if p.State == "confirmed" {
			return nil, ErrConflict("this proposal is already task " + p.TaskID + "; cancel the task instead")
		}
		if err := s.PMThread.Dismiss(id); err != nil {
			return nil, err
		}
		return fiber.Map{"proposalId": id, "state": "dismissed"}, nil
	})
}

func (s *Service) handlePMReset(c fiber.Ctx) error {
	if !s.pmOn() {
		return hidden(c)
	}
	return s.mutate(c, "aiagent.pmReset", nil, func() (any, error) {
		if err := s.PMThread.Reset(); err != nil {
			if errors.Is(err, planner.ErrBusy) {
				return nil, ErrConflict("the PM is still answering; reset when it is done")
			}
			return nil, err
		}
		return fiber.Map{"reset": true}, nil
	})
}
