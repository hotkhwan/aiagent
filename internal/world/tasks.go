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
	api.Post("/tasks/:id/delete", s.handleTaskDelete)
	api.Post("/tasks/:id/dispatch", s.handleTaskDispatch)
	api.Post("/tasks/:id/attempts/:aid/confirm", s.handleAttemptConfirm)
	api.Get("/tasks/:id/review", s.handleTaskReview)
	api.Post("/tasks/:id/advisory", s.handleTaskAdvisory)
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
		return s.fail(c, ErrNotFound("ไม่พบงานนี้"))
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
		return c.Status(fiber.StatusBadRequest).JSON(failEnv("AIAGENT_BAD_REQUEST", "ข้อมูลที่ส่งมาไม่ใช่ JSON ที่ถูกต้อง"))
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
		return c.Status(fiber.StatusBadRequest).JSON(failEnv("AIAGENT_BAD_REQUEST", "ข้อมูลที่ส่งมาไม่ใช่ JSON ที่ถูกต้อง"))
	}
	if len(body.Reason) > maxTaskText {
		return s.fail(c, ErrBadRequest("เหตุผลยาวเกินไป"))
	}
	return s.mutate(c, "aiagent.taskTransition", []string{id, string(body.To), body.IdempotencyKey}, func() (any, error) {
		if _, ok := s.Tasks.Get(id); !ok {
			return nil, ErrNotFound("ไม่พบงานนี้")
		}
		if body.To == taskstore.StatusCompleted && s.Orch != nil {
			moved, err := s.Orch.CheckBase(c.Context(), id)
			if err != nil {
				return nil, ErrConflict(err.Error())
			}
			if moved {
				return nil, ErrConflict("branch หลักเปลี่ยนไปหลังการตรวจ จะตรวจซ้ำก่อนปิดงาน")
			}
		}
		t, err := s.Tasks.Transition(id, body.To, taskstore.ActorUser, taskstore.Change{Reason: body.Reason}, body.IdempotencyKey)
		if err != nil {
			return nil, ErrConflict(err.Error())
		}
		return t, nil
	})
}

func (s *Service) handleTaskDelete(c fiber.Ctx) error {
	if s.Tasks == nil {
		return hidden(c)
	}
	id := c.Params("id")
	return s.mutate(c, "aiagent.taskDelete", []string{id}, func() (any, error) {
		if _, ok := s.Tasks.Get(id); !ok {
			return nil, ErrNotFound("ไม่พบงานนี้")
		}
		if err := s.Tasks.Delete(id, taskstore.ActorUser); err != nil {
			return nil, ErrConflict(err.Error())
		}
		return fiber.Map{"taskId": id, "deleted": true}, nil
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
		return ErrBadRequest("ชื่องานหรือโปรเจกต์ยาวเกินไป")
	}
	for _, list := range [][]string{nt.AcceptanceCriteria, nt.AllowedPaths, nt.BlockedBy, nt.RiskLabels} {
		if len(list) > maxTaskItems {
			return ErrBadRequest("รายการมีจำนวนข้อมากเกินไป")
		}
		for _, x := range list {
			if len(x) > maxTaskText {
				return ErrBadRequest("มีข้อที่ยาวเกินไป")
			}
		}
	}
	switch strings.TrimSpace(nt.Provider) {
	case "", "claude", "codex", "gemini":
	default:
		return ErrBadRequest("provider ต้องเป็น claude, codex, gemini หรือเว้นว่าง")
	}
	return nil
}
