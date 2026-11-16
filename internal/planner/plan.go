// Package planner is the central PM: a model that reads the user's request
// and proposes tasks, and reads a finished attempt and writes advice about it.
//
// It has no hands. A plan is data the user reads and confirms one task at a
// time; advice is a note beside the diff the user decides on. Nothing in this
// package creates a task, dispatches work or approves anything, and whatever
// the model returns is checked field by field before it is kept — a model's
// answer is untrusted input like any other.
//
// The model is reached through a subscription CLI (see Cloud), never through a
// provider API key. JSON keys are camelCase, like the rest of the API.
package planner

import (
	"errors"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"
)

// Limits on what a plan may hold. They sit at or under the task API's own, so
// a proposal that passes here can always be confirmed.
const (
	maxProposals = 6
	maxQuestions = 5
	maxCriteria  = 8
	maxPaths     = 12
	maxTitle     = 300  // bytes, the task API's limit
	maxItem      = 600  // runes in one criterion, path, reason or question
	maxReply     = 2000 // runes
	maxConcerns  = 10
)

var (
	// Kinds are the task kinds the store knows.
	Kinds = []string{"bug", "feature", "ux", "regression", "security", "chore"}
	// Providers are the agent CLIs work can be routed to.
	Providers = []string{"claude", "codex", "gemini"}
	// RiskLabels mark work the user must look at closely whatever a reviewer says.
	RiskLabels = []string{"auth", "authz", "crypto", "migration", "secretLeak", "deletesData", "ci", "deploy"}
	// Severities grade a reviewer's concern. None of them is a verdict.
	Severities = []string{"info", "warn", "risk"}

	slugRE   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,62}$`)
	taskIDRE = regexp.MustCompile(`^T-[0-9]{4,}$`)
)

// Turn is one earlier message of the conversation.
type Turn struct {
	Role string `json:"role"` // user | pm
	Text string `json:"text"`
}

// TaskBrief is a task as the PM is shown it.
type TaskBrief struct {
	ID        string `json:"taskId"`
	Title     string `json:"title"`
	Status    string `json:"status"`
	ProjectID string `json:"projectId"`
	Provider  string `json:"provider,omitempty"`
}

// AgentBrief is one agent CLI and whether it can take work right now.
type AgentBrief struct {
	Provider string `json:"provider"`
	State    string `json:"state"` // ready, notReady or notConfigured
}

// PlanInput is everything the PM is given to plan from.
type PlanInput struct {
	Request  string       `json:"request"`
	History  []Turn       `json:"history,omitempty"`
	Projects []string     `json:"projects"`
	Tasks    []TaskBrief  `json:"tasks"`
	Agents   []AgentBrief `json:"agents"`
}

// Proposal is one task the PM suggests. It becomes a task only when the user
// confirms it.
type Proposal struct {
	Title              string   `json:"title"`
	ProjectID          string   `json:"projectId"`
	Kind               string   `json:"kind"`
	Provider           string   `json:"provider"` // "" lets routing choose
	ProviderReason     string   `json:"providerReason,omitempty"`
	AcceptanceCriteria []string `json:"acceptanceCriteria"`
	AllowedPaths       []string `json:"allowedPaths,omitempty"`
	RiskLabels         []string `json:"riskLabels,omitempty"`
	DependsOn          []int    `json:"dependsOn,omitempty"` // 1-based, earlier proposals of the same plan
	BlockedBy          []string `json:"blockedBy,omitempty"` // existing task ids
}

// Plan is the PM's answer to a request.
type Plan struct {
	Reply     string     `json:"reply"`
	Proposals []Proposal `json:"proposals"`
	Questions []string   `json:"questions"`
}

func tidy(s string) string { return strings.TrimSpace(s) }

func tidyAll(xs []string) []string {
	out := make([]string, 0, len(xs))
	for _, x := range xs {
		out = append(out, tidy(x))
	}
	return out
}

func checkText(what, s string, max int) error {
	if s == "" {
		return fmt.Errorf("%s is empty", what)
	}
	if !utf8.ValidString(s) || utf8.RuneCountInString(s) > max {
		return fmt.Errorf("%s is longer than %d characters or not valid text", what, max)
	}
	return nil
}

// CheckPath accepts a repository-relative pattern: no root, no climbing out.
func CheckPath(p string) error {
	if p == "" || strings.HasPrefix(p, "/") || strings.HasPrefix(p, "~") || strings.Contains(p, "\\") || strings.ContainsAny(p, "\x00\n") {
		return fmt.Errorf("allowedPaths %q is not a repository-relative path", p)
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." {
			return fmt.Errorf("allowedPaths %q leaves the repository", p)
		}
	}
	if _, err := path.Match(strings.TrimSuffix(p, "/**"), ""); err != nil {
		return fmt.Errorf("allowedPaths %q is not a valid pattern", p)
	}
	return nil
}

// Check refuses a plan the task store could not hold or the user could not
// act on, and tidies the one it accepts in place. in is what the model was
// shown: projects and task ids it names must come from there.
func (p *Plan) Check(in PlanInput) error {
	p.Reply = tidy(p.Reply)
	p.Questions = tidyAll(p.Questions)
	if p.Reply == "" && len(p.Proposals) == 0 && len(p.Questions) == 0 {
		return errors.New("the plan is empty: give a reply, proposals or questions")
	}
	if utf8.RuneCountInString(p.Reply) > maxReply {
		return fmt.Errorf("reply is longer than %d characters", maxReply)
	}
	if len(p.Proposals) > maxProposals {
		return fmt.Errorf("%d proposals; at most %d — propose the next steps, not the whole roadmap", len(p.Proposals), maxProposals)
	}
	if len(p.Questions) > maxQuestions {
		return fmt.Errorf("%d questions; at most %d", len(p.Questions), maxQuestions)
	}
	for i, q := range p.Questions {
		if err := checkText(fmt.Sprintf("questions[%d]", i), q, maxItem); err != nil {
			return err
		}
	}
	known := map[string]bool{}
	for _, t := range in.Tasks {
		known[t.ID] = true
	}
	for i := range p.Proposals {
		if err := p.Proposals[i].check(i, in.Projects, known); err != nil {
			return err
		}
	}
	return nil
}

func (pr *Proposal) check(i int, projects []string, knownTasks map[string]bool) error {
	at := func(field string) string { return fmt.Sprintf("proposals[%d].%s", i, field) }
	pr.Title, pr.ProjectID, pr.Kind = tidy(pr.Title), tidy(pr.ProjectID), tidy(pr.Kind)
	pr.Provider, pr.ProviderReason = tidy(pr.Provider), tidy(pr.ProviderReason)
	pr.AcceptanceCriteria, pr.AllowedPaths = tidyAll(pr.AcceptanceCriteria), tidyAll(pr.AllowedPaths)
	pr.RiskLabels, pr.BlockedBy = tidyAll(pr.RiskLabels), tidyAll(pr.BlockedBy)

	if err := checkText(at("title"), pr.Title, maxItem); err != nil {
		return err
	}
	if len(pr.Title) > maxTitle || strings.ContainsAny(pr.Title, "\r\n") {
		return fmt.Errorf("%s must be one line of at most %d bytes (about 100 Thai characters)", at("title"), maxTitle)
	}
	switch {
	case len(projects) > 0 && !slices.Contains(projects, pr.ProjectID):
		return fmt.Errorf("%s %q is not one of the known projects: %s", at("projectId"), pr.ProjectID, strings.Join(projects, ", "))
	case !slugRE.MatchString(pr.ProjectID):
		return fmt.Errorf("%s %q must be a plain name (letters, digits, . _ -)", at("projectId"), pr.ProjectID)
	}
	if !slices.Contains(Kinds, pr.Kind) {
		return fmt.Errorf("%s %q must be one of %s", at("kind"), pr.Kind, strings.Join(Kinds, ", "))
	}
	if pr.Provider == "auto" {
		pr.Provider = ""
	}
	if pr.Provider != "" && !slices.Contains(Providers, pr.Provider) {
		return fmt.Errorf("%s %q must be auto, %s", at("provider"), pr.Provider, strings.Join(Providers, ", "))
	}
	if utf8.RuneCountInString(pr.ProviderReason) > maxItem {
		return fmt.Errorf("%s is too long", at("providerReason"))
	}
	if n := len(pr.AcceptanceCriteria); n == 0 || n > maxCriteria {
		return fmt.Errorf("%s needs 1 to %d items: without one the work cannot be verified", at("acceptanceCriteria"), maxCriteria)
	}
	for _, c := range pr.AcceptanceCriteria {
		if err := checkText(at("acceptanceCriteria")+" item", c, maxItem); err != nil {
			return err
		}
	}
	if len(pr.AllowedPaths) > maxPaths {
		return fmt.Errorf("%s has more than %d items", at("allowedPaths"), maxPaths)
	}
	for _, ap := range pr.AllowedPaths {
		if err := CheckPath(ap); err != nil {
			return fmt.Errorf("%s: %w", at("allowedPaths"), err)
		}
	}
	for _, r := range pr.RiskLabels {
		if !slices.Contains(RiskLabels, r) {
			return fmt.Errorf("%s %q must be one of %s", at("riskLabels"), r, strings.Join(RiskLabels, ", "))
		}
	}
	seen := map[int]bool{}
	for _, d := range pr.DependsOn {
		if d < 1 || d > i || seen[d] {
			return fmt.Errorf("%s %d must name an earlier proposal of this plan (1 to %d), once", at("dependsOn"), d, i)
		}
		seen[d] = true
	}
	if len(pr.BlockedBy) > maxPaths {
		return fmt.Errorf("%s has more than %d items", at("blockedBy"), maxPaths)
	}
	for _, b := range pr.BlockedBy {
		if !taskIDRE.MatchString(b) || !knownTasks[b] {
			return fmt.Errorf("%s %q is not an existing task id", at("blockedBy"), b)
		}
	}
	return nil
}

// FileStat is one changed file of the attempt under review.
type FileStat struct {
	Path      string `json:"path"`
	Added     int    `json:"added"`
	Deleted   int    `json:"deleted"`
	Binary    bool   `json:"binary,omitempty"`
	Protected bool   `json:"protected,omitempty"`
}

// ReviewInput is what the reviewer is shown about one attempt.
type ReviewInput struct {
	TaskID             string     `json:"taskId"`
	Title              string     `json:"title"`
	Kind               string     `json:"kind,omitempty"`
	AcceptanceCriteria []string   `json:"acceptanceCriteria"`
	AllowedPaths       []string   `json:"allowedPaths,omitempty"`
	RiskLabels         []string   `json:"riskLabels,omitempty"`
	Tests              string     `json:"tests"`
	Files              []FileStat `json:"files"`
	Diff               string     `json:"diff"`
	DiffTruncated      bool       `json:"diffTruncated,omitempty"`
}

// Concern is one thing the reviewer thinks the user should look at.
type Concern struct {
	Severity string `json:"severity"` // info | warn | risk
	File     string `json:"file,omitempty"`
	Note     string `json:"note"`
}

// Advice is a reviewer's note on an attempt. It has no verdict field on
// purpose: the reviewer advises, the user decides.
type Advice struct {
	Summary  string    `json:"summary"`
	Concerns []Concern `json:"concerns"`
	Checks   []string  `json:"checks"` // things worth trying by hand before deciding
}

// Check refuses advice that is empty or malformed, and tidies what it accepts.
func (a *Advice) Check() error {
	a.Summary = tidy(a.Summary)
	a.Checks = tidyAll(a.Checks)
	if err := checkText("summary", a.Summary, maxReply); err != nil {
		return err
	}
	if len(a.Concerns) > maxConcerns || len(a.Checks) > maxConcerns {
		return fmt.Errorf("at most %d concerns and %d checks", maxConcerns, maxConcerns)
	}
	for i := range a.Concerns {
		c := &a.Concerns[i]
		c.Severity, c.File, c.Note = tidy(c.Severity), tidy(c.File), tidy(c.Note)
		if !slices.Contains(Severities, c.Severity) {
			return fmt.Errorf("concerns[%d].severity %q must be one of %s", i, c.Severity, strings.Join(Severities, ", "))
		}
		if err := checkText(fmt.Sprintf("concerns[%d].note", i), c.Note, maxItem); err != nil {
			return err
		}
		if utf8.RuneCountInString(c.File) > maxItem {
			return fmt.Errorf("concerns[%d].file is too long", i)
		}
	}
	for i, c := range a.Checks {
		if err := checkText(fmt.Sprintf("checks[%d]", i), c, maxItem); err != nil {
			return err
		}
	}
	return nil
}
