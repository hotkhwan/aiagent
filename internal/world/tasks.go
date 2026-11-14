// internal/world/tasks.go
//
// The task API: the board and the PM read tasks here, and the user's own
// moves — create, cancel, approve, ask for a revision — go through it. The
// orchestrator's moves (ready, start, verify) are not reachable from HTTP at
// all; the API always acts as the user, and the state machine in
// internal/taskstore decides what the user may do.
package world

import (
	"strings"

	"github.com/gofiber/fiber/v3"

	"github.com/hotkhwan/aiagent/internal/taskstore"
)

func (s *Service) mountTasks(api fiber.Router) {
	api.Get("/tasks", s.handleTaskList)
	api.Get("/tasks/:id", s.handleTaskGet)
	api.Post("/tasks", s.handleTaskCreate)
	api.Post("/tasks/:id/transition", s.handleTaskTransition)
}

func (s *Service) handleTaskList(c fiber.Ctx) error {
	if s.Tasks == nil {
		return hidden(c)
	}
	return c.JSON(okEnv(fiber.Map{"tasks": s.Tasks.List()}))
}

func (s *Service) handleTaskGet(c fiber.Ctx) error {
	if s.Tasks == nil {
		return hidden(c)
	}
	t, ok := s.Tasks.Get(c.Params("id"))
	if !ok {
		return s.fail(c, ErrNotFound("no such task"))
	}
	events := s.Tasks.Events(t.ID)
	if events == nil {
		events = []taskstore.Event{}
	}
	return c.JSON(okEnv(fiber.Map{"task": t, "events": events}))
}

func (s *Service) handleTaskCreate(c fiber.Ctx) error {
	if s.Tasks == nil {
		return hidden(c)
	}
	var body struct {
		taskstore.NewTask
		IdempotencyKey string `json:"idempotencyKey"`
	}
	if err := c.Bind().Body(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(failEnv("AIAGENT_BAD_REQUEST", "invalid JSON body"))
	}
	if err := checkTaskText(body.NewTask); err != nil {
		return s.fail(c, err)
	}
	return s.mutate(c, "aiagent.taskCreate", []string{body.ProjectID, body.Title, body.IdempotencyKey}, func() (any, error) {
		t, err := s.Tasks.Create(body.NewTask, taskstore.ActorUser, body.IdempotencyKey)
		if err != nil {
			return nil, ErrBadRequest(err.Error())
		}
		return t, nil
	})
}

func (s *Service) handleTaskTransition(c fiber.Ctx) error {
	if s.Tasks == nil {
		return hidden(c)
	}
	id := c.Params("id")
	var body struct {
		To             taskstore.Status `json:"to"`
		Reason         string           `json:"reason"`
		IdempotencyKey string           `json:"idempotencyKey"`
	}
	if err := c.Bind().Body(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(failEnv("AIAGENT_BAD_REQUEST", "invalid JSON body"))
	}
	if len(body.Reason) > maxTaskText {
		return s.fail(c, ErrBadRequest("reason is too long"))
	}
	return s.mutate(c, "aiagent.taskTransition", []string{id, string(body.To), body.IdempotencyKey}, func() (any, error) {
		if _, ok := s.Tasks.Get(id); !ok {
			return nil, ErrNotFound("no such task")
		}
		t, err := s.Tasks.Transition(id, body.To, taskstore.ActorUser, taskstore.Change{Reason: body.Reason}, body.IdempotencyKey)
		if err != nil {
			return nil, ErrConflict(err.Error())
		}
		return t, nil
	})
}

const (
	maxTaskText  = 4000
	maxTaskItems = 50
)

// checkTaskText bounds what a client can put in a task. The task log is
// served back to every open tab, so it is kept to a sane size.
func checkTaskText(nt taskstore.NewTask) error {
	if len(nt.Title) > 300 || len(nt.ProjectID) > 100 {
		return ErrBadRequest("title or projectId is too long")
	}
	for _, list := range [][]string{nt.AcceptanceCriteria, nt.AllowedPaths, nt.BlockedBy, nt.RiskLabels} {
		if len(list) > maxTaskItems {
			return ErrBadRequest("too many items in a list")
		}
		for _, x := range list {
			if len(x) > maxTaskText {
				return ErrBadRequest("an item is too long")
			}
		}
	}
	switch strings.TrimSpace(nt.Provider) {
	case "", "claude", "codex", "gemini":
	default:
		return ErrBadRequest("provider must be claude, codex, gemini or empty")
	}
	return nil
}
