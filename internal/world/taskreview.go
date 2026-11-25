// internal/world/taskreview.go
//
// The review page: everything the user needs to decide on a finished attempt,
// read from the checkout rather than taken from the agent's own report.
//
// It decides nothing. Approving and sending back stay the user's moves on
// /tasks/:id/transition; this file only reads. The optional second opinion is
// written by the PM model on request and is advice — it has no verdict, and
// asking for it never moves the task.
package world

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"

	"github.com/hotkhwan/aiagent/internal/orchestrator"
	"github.com/hotkhwan/aiagent/internal/planner"
	"github.com/hotkhwan/aiagent/internal/taskstore"
)

// pmCallTimeout bounds one background PM job: three tries of three minutes
// and some slack.
const pmCallTimeout = 10 * time.Minute

type reviewBase struct {
	Commit         string `json:"commit,omitempty"`
	State          string `json:"state"` // current | behind | diverged | unknown
	MainlineRef    string `json:"mainlineRef,omitempty"`
	MainlineCommit string `json:"mainlineCommit,omitempty"`
}

type reviewState struct {
	State  string `json:"state"`
	Detail string `json:"detail,omitempty"`
}

type taskReview struct {
	Task              taskstore.Task        `json:"task"`
	Attempt           *taskstore.Attempt    `json:"attempt"`
	Evidence          map[string]string     `json:"evidence"`
	Files             []planner.FileStat    `json:"files"`
	Diff              string                `json:"diff"`
	DiffTruncated     bool                  `json:"diffTruncated"`
	DiffError         string                `json:"diffError,omitempty"`
	Base              reviewBase            `json:"base"`
	Tests             reviewState           `json:"tests"` // passed | notRun | unknown
	Scan              reviewState           `json:"scan"`  // notScanned: no scanner runs yet
	Advisory          *planner.AdvisoryView `json:"advisory"`
	AdvisoryAvailable bool                  `json:"advisoryAvailable"`
}

// reviewAttempt is the attempt whose work is under review: the last one that
// reported a commit.
func reviewAttempt(t taskstore.Task) *taskstore.Attempt {
	var a *taskstore.Attempt
	for i := range t.Attempts {
		if t.Attempts[i].Status == taskstore.AttemptReported && t.Attempts[i].Commit != "" {
			a = &t.Attempts[i]
		}
	}
	return a
}

// reviewEvidence is what verification recorded when it handed the task over.
func (s *Service) reviewEvidence(taskID string) map[string]string {
	evidence := map[string]string{}
	for _, e := range s.Tasks.Events(taskID) {
		var d struct {
			To       taskstore.Status  `json:"to"`
			Evidence map[string]string `json:"evidence"`
		}
		if json.Unmarshal(e.Data, &d) == nil && d.To == taskstore.StatusAwaitingReview && d.Evidence != nil {
			evidence = d.Evidence
		}
	}
	return evidence
}

func testsState(evidence map[string]string) reviewState {
	v := evidence["tests"]
	if cmd, ok := strings.CutPrefix(v, "passed: "); ok {
		return reviewState{State: "passed", Detail: cmd}
	}
	if why, ok := strings.CutPrefix(v, "notRun: "); ok {
		return reviewState{State: "notRun", Detail: why}
	}
	return reviewState{State: "unknown", Detail: v}
}

func (s *Service) buildReview(t taskstore.Task) taskReview {
	rv := taskReview{
		Task:              t,
		Attempt:           reviewAttempt(t),
		Evidence:          s.reviewEvidence(t.ID),
		Files:             []planner.FileStat{},
		Base:              reviewBase{State: "unknown"},
		Scan:              reviewState{State: "notScanned"},
		AdvisoryAvailable: s.PM != nil && s.PMThread != nil,
	}
	rv.Tests = testsState(rv.Evidence)
	a := rv.Attempt
	if a == nil {
		rv.DiffError = "no attempt has reported a commit yet"
		return rv
	}
	rv.Base.Commit = a.BaseCommit
	if rv.AdvisoryAvailable {
		if adv, ok := s.PMThread.Advisory(a.ID); ok {
			rv.Advisory = &adv
		}
	}
	ws, ok := s.Collector.lookupWorkspace(a.Workspace)
	if !ok {
		rv.DiffError = "workspace " + a.Workspace + " is no longer configured, so its checkout cannot be read"
		return rv
	}
	g := RepoGit{R: s.Collector.Runner}
	files, err := g.DiffStat(ws.dir, a.BaseCommit, a.Commit)
	if err != nil {
		rv.DiffError = err.Error()
		return rv
	}
	for i := range files {
		files[i].Protected = orchestrator.IsProtected(files[i].Path)
	}
	rv.Files = files
	if rv.Diff, rv.DiffTruncated, err = g.Diff(ws.dir, a.BaseCommit, a.Commit); err != nil {
		rv.DiffError = err.Error()
	}
	if ref, sha, ok := g.Mainline(ws.dir); ok {
		rv.Base.MainlineRef, rv.Base.MainlineCommit = ref, sha
		switch {
		case sha == a.BaseCommit:
			rv.Base.State = "current"
		case g.IsAncestor(ws.dir, a.BaseCommit, sha):
			rv.Base.State = "behind"
		default:
			rv.Base.State = "diverged"
		}
	}
	return rv
}

func (s *Service) handleTaskReview(c fiber.Ctx) error {
	if s.Tasks == nil {
		return hidden(c)
	}
	t, ok := s.Tasks.Get(c.Params("id"))
	if !ok {
		return s.fail(c, ErrNotFound("ไม่พบงานนี้"))
	}
	return c.JSON(okEnv(s.buildReview(t)))
}

func redactAdvice(a *planner.Advice) {
	a.Summary = Redact(a.Summary)
	for i := range a.Concerns {
		a.Concerns[i].File, a.Concerns[i].Note = Redact(a.Concerns[i].File), Redact(a.Concerns[i].Note)
	}
	for i := range a.Checks {
		a.Checks[i] = Redact(a.Checks[i])
	}
}

// handleTaskAdvisory asks the PM model for a second opinion on the attempt
// under review. The call takes longer than a request should, so it runs in
// the background and the review page shows it when it lands.
func (s *Service) handleTaskAdvisory(c fiber.Ctx) error {
	if s.Tasks == nil || s.PM == nil || s.PMThread == nil {
		return hidden(c)
	}
	id := c.Params("id")
	return s.mutate(c, "aiagent.taskAdvisory", []string{id}, func() (any, error) {
		t, ok := s.Tasks.Get(id)
		if !ok {
			return nil, ErrNotFound("ไม่พบงานนี้")
		}
		rv := s.buildReview(t)
		if rv.Attempt == nil || rv.DiffError != "" {
			return nil, ErrConflict("ไม่มี diff ที่อ่านได้ให้รีวิว: " + rv.DiffError)
		}
		key := rv.Attempt.ID
		if err := s.PMThread.AdvisoryStart(key); err != nil {
			if errors.Is(err, planner.ErrBusy) {
				return nil, ErrConflict("กำลังเขียนบันทึกรีวิวของรอบนี้อยู่แล้ว")
			}
			return nil, err
		}
		in := planner.ReviewInput{
			TaskID: t.ID, Title: t.Title, Kind: t.Kind,
			AcceptanceCriteria: t.AcceptanceCriteria, AllowedPaths: t.AllowedPaths, RiskLabels: t.RiskLabels,
			Tests: rv.Evidence["tests"], Files: rv.Files, Diff: rv.Diff, DiffTruncated: rv.DiffTruncated,
		}
		s.background(func() {
			ctx, cancel := context.WithTimeout(context.Background(), pmCallTimeout)
			defer cancel()
			adv, call, err := s.PM.Advise(ctx, in)
			if err != nil {
				_ = s.PMThread.AdvisoryFail(key, truncateRunes(Redact(err.Error()), 500), call)
				return
			}
			redactAdvice(&adv)
			_ = s.PMThread.AdvisoryDone(key, adv, call)
		}, func() {
			_ = s.PMThread.AdvisoryFail(key, pmCrashed, planner.Call{Outcome: "failed"})
		})
		return fiber.Map{"attemptId": key, "state": "running"}, nil
	})
}
