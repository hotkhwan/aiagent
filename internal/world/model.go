// internal/aiagent/model.go
package world

import "time"

// Session status values. They are DERIVED from observed evidence only:
//
//	running  — process proven alive AND its CLI reports work in progress
//	idle     — process proven alive, CLI waiting for input
//	ended    — no live process holds the session (exited); history only
//
// A session is never reported "completed" here: process exit is not proof
// of a successful task. Task outcome lives on agcmd eng runs (self-reported,
// labelled as such).
const (
	StatusRunning = "running"
	StatusIdle    = "idle"
	StatusEnded   = "ended"
)

// Activity types the UI maps to rooms. Classified server-side from tool
// names / command keywords; tool inputs are never sent to the browser.
const (
	ActivityCodeEdit = "codeEdit"
	ActivityTestRun  = "testRun"
	ActivityDeploy   = "deploy"
	ActivityResearch = "research"
	ActivityCommand  = "command"
	ActivityPlanning = "planning"
	ActivityIdle     = "idle"
	ActivityUnknown  = "unknown"
)

// Usage is cumulative token usage for a session. Complete=false means the
// transcript is still being counted (large files are read incrementally) and
// the numbers are a lower bound the UI must not present as final.
type Usage struct {
	InputTokens  int64  `json:"inputTokens"`
	OutputTokens int64  `json:"outputTokens"`
	CachedTokens int64  `json:"cachedTokens"`
	Complete     bool   `json:"complete"`
	Source       string `json:"source"`
}

// PRLink is a pull request the CLI recorded for the session.
type PRLink struct {
	Number     int    `json:"number"`
	URL        string `json:"url"`
	Repository string `json:"repository"`
}

// TimelineItem is one tool step (name + classification only, no content).
type TimelineItem struct {
	At           time.Time `json:"at"`
	Tool         string    `json:"tool"`
	ActivityType string    `json:"activityType"`
}

// Session is one AI CLI session observed on this host.
type Session struct {
	SessionID   string `json:"sessionId"`
	RuntimeType string `json:"runtimeType"` // claudeCode | codexCli
	ProviderID  string `json:"providerId"`  // anthropic | openai
	DisplayName string `json:"displayName"`
	Owner       string `json:"owner"`
	Cwd         string `json:"cwd"`
	RepoID      string `json:"repoId"`
	Workspace   string `json:"workspace,omitempty"`
	// DispatchVia names the agcmd workspace whose tmux agent pane IS this
	// session's process (pid descends from the pane). Only then does sending
	// work from the web reach this exact session.
	DispatchVia  string     `json:"dispatchVia,omitempty"`
	GitBranch    string     `json:"gitBranch,omitempty"`
	Title        string     `json:"title,omitempty"`
	Status       string     `json:"status"`
	Live         bool       `json:"live"`
	LiveEvidence string     `json:"liveEvidence"`
	Pid          int        `json:"pid,omitempty"`
	ActivityType string     `json:"activityType"`
	LastTool     string     `json:"lastTool,omitempty"`
	StartedAt    *time.Time `json:"startedAt,omitempty"`
	LastEventAt  *time.Time `json:"lastEventAt,omitempty"`
	Usage        Usage      `json:"usage"`
	// Kind is Claude Code's own session kind ("bg" | "interactive").
	Kind string `json:"kind,omitempty"`
	// AgentState is the agent-view state from `claude agents --json`
	// (working | blocked | done) when the daemon knows this session.
	AgentState string `json:"agentState,omitempty"`
	// StopVia is the supported way the web may stop this agent:
	// "claudeStop" (`claude stop <id>`, conversation kept) or "agcmdRestart"
	// (`agcmd restart <workspace>` for a CLI running in a agcmd tmux pane).
	// Empty → StopBlocked explains why it must be stopped where it runs.
	StopVia     string `json:"stopVia,omitempty"`
	StopBlocked string `json:"stopBlocked,omitempty"`
	// MessageVia: "agcmd" | "relay" — how the web can type to THIS session.
	MessageVia     string     `json:"messageVia,omitempty"`
	MessageBlocked string     `json:"messageBlocked,omitempty"`
	PRs            []PRLink   `json:"prs,omitempty"`
	SubAgents      []SubAgent `json:"subAgents,omitempty"`
	Watches        []Watch    `json:"watches,omitempty"`
	LastCI         *Watch     `json:"lastCI,omitempty"`
	// Model is the model id of the session's latest reply (e.g. claude-opus-5-5).
	Model string `json:"model,omitempty"`
	// Doing is a background job's live one-line status ("what it is doing
	// now"), from ~/.claude/jobs/<short>/timeline.jsonl. Progress notes the
	// agent writes between tool calls are streamed, not saved, so this is the
	// closest on-disk signal.
	Doing *JobDoing `json:"doing,omitempty"`

	timeline   []TimelineItem
	sourcePath string
	home       string
	inPane     string
}

// Decision is a agcmd eng question waiting for a human.
type Decision struct {
	Question string   `json:"question"`
	Options  []string `json:"options"`
}

// Run is a agcmd eng run (mission). Status is SELF-REPORTED by the agent or an
// operator through agcmd; Lost is derived (RUNNING but its tmux session gone).
type Run struct {
	ID          string    `json:"id"`
	Task        string    `json:"task"`
	Workspace   string    `json:"workspace"`
	TmuxSession string    `json:"tmuxSession"`
	Status      string    `json:"status"`
	CreatedAt   string    `json:"createdAt"`
	UpdatedAt   string    `json:"updatedAt"`
	Decision    *Decision `json:"decision,omitempty"`
	TmuxAlive   bool      `json:"tmuxAlive"`
	Lost        bool      `json:"lost"`
}

// Workspace is a agcmd workspace an operator may dispatch to.
type Workspace struct {
	Name         string     `json:"name"`
	TmuxSession  string     `json:"tmuxSession"`
	Dir          string     `json:"dir"`
	DirExists    bool       `json:"dirExists"`
	TmuxAlive    bool       `json:"tmuxAlive"`
	AgentCommand string     `json:"agentCommand,omitempty"`
	AgentReady   bool       `json:"agentReady"`
	LastActivity *time.Time `json:"lastActivity,omitempty"`
}

// Quota is one provider allowance reading. Confidence is "actual" only when
// the provider itself reported it; otherwise "unknown" or "stale" and the
// percent fields stay nil — never estimated.
type Quota struct {
	ProviderID       string     `json:"providerId"`
	Mode             string     `json:"mode"` // normal | eco | critical | reserve | guarded
	Confidence       string     `json:"confidence"`
	UsedPercent      *float64   `json:"usedPercent"`
	RemainingPercent *float64   `json:"remainingPercent"`
	WindowMinutes    int        `json:"windowMinutes,omitempty"`
	ResetsAt         *time.Time `json:"resetsAt,omitempty"`
	ObservedAt       *time.Time `json:"observedAt,omitempty"`
	PlanType         string     `json:"planType,omitempty"`
	Source           string     `json:"source"`
}

// Summary is the "today" counter row.
type Summary struct {
	Running          int `json:"running"`
	NeedsInput       int `json:"needsInput"`
	Completed        int `json:"completed"`
	Idle             int `json:"idle"`
	WaitingApproval  int `json:"waitingApproval"`
	Lost             int `json:"lost"`
	EndedToday       int `json:"endedToday"`
	RunsDoneToday    int `json:"runsDoneToday"`
	RunsAbortedToday int `json:"runsAbortedToday"`
}

// Snapshot is the whole monitored state at one instant.
type Snapshot struct {
	GeneratedAt time.Time   `json:"generatedAt"`
	Host        string      `json:"host"`
	Summary     Summary     `json:"summary"`
	Sessions    []Session   `json:"sessions"`
	Runs        []Run       `json:"runs"`
	Workspaces  []Workspace `json:"workspaces"`
	Quotas      []Quota     `json:"quotas"`
	Warnings    []string    `json:"warnings"`
}
