// internal/world/kcmd.go
//
// The kcmd driver is one write path: AI Agent World never types into tmux
// itself here and never runs a shell. Every action is
// `kcmd <fixed subcommand> <args...>` executed with an argv (no shell), after:
//
//   - the workspace resolves to a configured entry (see workspaces.go); an
//     unknown name is refused and never reaches the command line,
//   - the text is length-limited and stripped of control characters,
//   - the target tmux `agent` window is confirmed to be running an agent CLI
//     (claude/codex/node), NOT a bare shell. kcmd would otherwise create an
//     empty session and type the operator's text into bash.
package world

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// workspaceEntry, the workspace list and the two lookups live in
// workspaces.go: they are configuration, not a compiled-in table.

// Runner executes a program with an argv. Tests substitute a fake.
type Runner interface {
	Run(ctx context.Context, name string, args ...string) ([]byte, error)
}

// ExecRunner runs real processes (never through a shell).
type ExecRunner struct{}

// Run implements Runner.
func (ExecRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(os.Environ(), "TERM=dumb")
	out, err := cmd.CombinedOutput()
	if len(out) > 64<<10 {
		out = out[:64<<10]
	}
	return out, err
}

// tmuxState is one read of the tmux server: alive sessions + what the agent
// window's active pane is running.
type tmuxState struct {
	sessions map[string]time.Time
	agentCmd map[string]string
	agentPid map[string]int
}

var shellCommands = map[string]bool{"bash": true, "sh": true, "zsh": true, "fish": true, "dash": true, "login": true, "tmux": true, "": true}

func (c *Collector) readTmux(ctx context.Context) tmuxState {
	st := tmuxState{sessions: map[string]time.Time{}, agentCmd: map[string]string{}, agentPid: map[string]int{}}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	out, err := c.Runner.Run(ctx, c.TmuxPath, "list-sessions", "-F", "#{session_name}\t#{session_activity}")
	if err != nil {
		return st // "no server running" → no sessions
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) != 2 || parts[0] == "" {
			continue
		}
		var at time.Time
		if sec, err := strconv.ParseInt(parts[1], 10, 64); err == nil {
			at = time.Unix(sec, 0).UTC()
		}
		st.sessions[parts[0]] = at
	}
	out, err = c.Runner.Run(ctx, c.TmuxPath, "list-panes", "-a", "-F", "#{session_name}\t#{window_name}\t#{pane_active}\t#{pane_current_command}\t#{pane_pid}")
	if err != nil {
		return st
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		p := strings.Split(line, "\t")
		if len(p) < 4 || p[1] != "agent" || p[2] != "1" {
			continue
		}
		st.agentCmd[p[0]] = p[3]
		if len(p) > 4 {
			if pid, err := strconv.Atoi(p[4]); err == nil {
				st.agentPid[p[0]] = pid
			}
		}
	}
	return st
}

func agentReady(cmd string) bool { return !shellCommands[cmd] }

func (c *Collector) workspaces(tm tmuxState) []Workspace {
	out := make([]Workspace, 0, len(c.Workspaces))
	for _, w := range c.Workspaces {
		ws := Workspace{Name: w.name, TmuxSession: w.session, Dir: w.dir}
		if fi, err := os.Stat(w.dir); err == nil && fi.IsDir() {
			ws.DirExists = true
		}
		if at, ok := tm.sessions[w.session]; ok {
			ws.TmuxAlive = true
			if !at.IsZero() {
				t := at
				ws.LastActivity = &t
			}
			ws.AgentCommand = tm.agentCmd[w.session]
			ws.AgentReady = agentReady(ws.AgentCommand)
		}
		out = append(out, ws)
	}
	return out
}

// ---- kcmd eng runs (read) ----

var runIDPattern = regexp.MustCompile(`^ENG-[0-9]{8}-[0-9]{3,}$`)

type runFile struct {
	ID        string    `json:"id"`
	Task      string    `json:"task"`
	Workspace string    `json:"workspace"`
	Session   string    `json:"session"`
	Status    string    `json:"status"`
	CreatedAt string    `json:"created_at"`
	UpdatedAt string    `json:"updated_at"`
	Decision  *Decision `json:"decision"`
}

func (c *Collector) readRuns(tm tmuxState) []Run {
	files, _ := filepath.Glob(filepath.Join(c.KcmdRunsDir, "ENG-*.json"))
	var out []Run
	for _, p := range files {
		rf, err := readRunFile(p)
		if err != nil || rf.Status == "ARCHIVED" {
			continue
		}
		_, alive := tm.sessions[rf.Session]
		out = append(out, Run{
			ID: rf.ID, Task: truncateRunes(rf.Task, 600), Workspace: rf.Workspace, TmuxSession: rf.Session,
			Status: rf.Status, CreatedAt: rf.CreatedAt, UpdatedAt: rf.UpdatedAt, Decision: rf.Decision,
			TmuxAlive: alive, Lost: rf.Status == "RUNNING" && !alive,
		})
	}
	return out
}

func readRunFile(p string) (runFile, error) {
	var rf runFile
	b, err := os.ReadFile(p)
	if err != nil {
		return rf, err
	}
	err = jsonUnmarshal(b, &rf)
	return rf, err
}

// RunLog returns the last n lines of a run's kcmd log (agent-written notes).
func (c *Collector) RunLog(id string, n int) ([]string, error) {
	if !runIDPattern.MatchString(id) {
		return nil, ErrBadRequest("invalid run id")
	}
	p := filepath.Join(c.KcmdRunsDir, id+".log")
	fi, err := os.Stat(p)
	if err != nil {
		return nil, ErrNotFound("run not found")
	}
	var lines []string
	for _, l := range readTail(p, fi.Size(), 64<<10) {
		if s := strings.TrimRight(string(l), "\r"); s != "" {
			lines = append(lines, truncateRunes(s, 500))
		}
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines, nil
}

// ---- actions (write, via kcmd only) ----

// APIError carries an HTTP status for the handler layer.
type APIError struct {
	Status int
	Code   string
	Msg    string
}

func (e *APIError) Error() string { return e.Msg }

// ErrBadRequest is a 400.
func ErrBadRequest(msg string) error { return &APIError{400, "AIAGENT_BAD_REQUEST", msg} }

// ErrNotFound is a 404.
func ErrNotFound(msg string) error { return &APIError{404, "AIAGENT_NOT_FOUND", msg} }

// ErrConflict is a 409 (target not in a state that can accept the action).
func ErrConflict(msg string) error { return &APIError{409, "AIAGENT_CONFLICT", msg} }

const maxTextRunes = 4000

// engReserved are words kcmd eng would treat as a subcommand, not task text.
var engReserved = map[string]bool{
	"status": true, "inbox": true, "open": true, "watch": true, "answer": true, "decision": true,
	"note": true, "set-status": true, "pause": true, "resume": true, "approve": true, "abort": true,
	"archive": true, "cleanup": true, "diff": true, "exec": true, "wait": true, "help": true,
}

// cleanText normalises operator text: CRLF→LF, control characters other than
// newline/tab removed, trimmed, 1..maxTextRunes runes, no leading '-'.
func cleanText(s string) (string, error) {
	if !utf8.ValidString(s) {
		return "", ErrBadRequest("text must be valid UTF-8")
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if unicode.IsControl(r) || r == ' ' || r == ' ' {
			return -1
		}
		return r
	}, s)
	s = strings.TrimSpace(s)
	n := utf8.RuneCountInString(s)
	if n == 0 {
		return "", ErrBadRequest("text is required")
	}
	if n > maxTextRunes {
		return "", ErrBadRequest(fmt.Sprintf("text is longer than %d characters", maxTextRunes))
	}
	if strings.HasPrefix(s, "-") {
		return "", ErrBadRequest("text must not start with '-'")
	}
	return s, nil
}

func (c *Collector) requireAgentReady(ctx context.Context, session string) error {
	tm := c.readTmux(ctx)
	if _, ok := tm.sessions[session]; !ok {
		return ErrConflict("tmux session " + session + " is not running — start the agent first (kcmd start)")
	}
	if cmd := tm.agentCmd[session]; !agentReady(cmd) {
		return ErrConflict("the agent window of " + session + " is a plain shell (" + cmd + "), not an AI agent — refusing to type into a shell")
	}
	return nil
}

func (c *Collector) kcmd(ctx context.Context, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := c.Runner.Run(ctx, c.KcmdPath, args...)
	text := strings.TrimSpace(string(out))
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) || text != "" {
			return text, &APIError{502, "AIAGENT_KCMD_FAILED", "kcmd failed: " + truncateRunes(text, 300)}
		}
		return text, &APIError{502, "AIAGENT_KCMD_FAILED", "kcmd failed: " + err.Error()}
	}
	return text, nil
}

var createdRunPattern = regexp.MustCompile(`(ENG-[0-9]{8}-[0-9]{3,})\s+RUNNING`)

// Mission creates a tracked kcmd eng run in a workspace.
func (c *Collector) Mission(ctx context.Context, workspace, task string) (string, error) {
	w, ok := c.lookupWorkspace(workspace)
	if !ok {
		return "", ErrBadRequest("unknown workspace")
	}
	task, err := cleanText(task)
	if err != nil {
		return "", err
	}
	if engReserved[strings.ToLower(task)] {
		return "", ErrBadRequest("task text is a reserved kcmd word; describe the task in a sentence")
	}
	if err := c.requireAgentReady(ctx, w.session); err != nil {
		return "", err
	}
	out, err := c.kcmd(ctx, "eng", task, w.name)
	if err != nil {
		return "", err
	}
	if m := createdRunPattern.FindStringSubmatch(out); m != nil {
		return m[1], nil
	}
	return "", nil
}

// Message types a free-form message into a workspace's running agent.
func (c *Collector) Message(ctx context.Context, workspace, message string) error {
	w, ok := c.lookupWorkspace(workspace)
	if !ok {
		return ErrBadRequest("unknown workspace")
	}
	message, err := cleanText(message)
	if err != nil {
		return err
	}
	if err := c.requireAgentReady(ctx, w.session); err != nil {
		return err
	}
	_, err = c.kcmd(ctx, "dispatch", w.name, message)
	return err
}

func (c *Collector) loadRun(id string) (runFile, error) {
	if !runIDPattern.MatchString(id) {
		return runFile{}, ErrBadRequest("invalid run id")
	}
	rf, err := readRunFile(filepath.Join(c.KcmdRunsDir, id+".json"))
	if err != nil {
		return rf, ErrNotFound("run not found")
	}
	return rf, nil
}

// Answer replies to a run waiting for a decision.
func (c *Collector) Answer(ctx context.Context, id, answer string) error {
	rf, err := c.loadRun(id)
	if err != nil {
		return err
	}
	if rf.Status != "WAITING_DECISION" {
		return ErrConflict("run is " + rf.Status + ", not waiting for a decision")
	}
	answer, err = cleanText(answer)
	if err != nil {
		return err
	}
	if err := c.requireAgentReady(ctx, rf.Session); err != nil {
		return err
	}
	_, err = c.kcmd(ctx, "eng", "answer", id, answer)
	return err
}

// SetRunStatus records approve (→READY) or abort (→ABORTED) on a run. It
// changes kcmd bookkeeping only; it does not merge, deploy or kill anything.
func (c *Collector) SetRunStatus(ctx context.Context, id, action string) error {
	rf, err := c.loadRun(id)
	if err != nil {
		return err
	}
	switch action {
	case "approve", "abort":
	default:
		return ErrBadRequest("unknown action")
	}
	switch rf.Status {
	case "DONE", "ABORTED", "ARCHIVED":
		return ErrConflict("run is already " + rf.Status)
	}
	_, err = c.kcmd(ctx, "eng", action, id)
	return err
}

var shortIDPattern = regexp.MustCompile(`^[0-9a-f]{8}$`)

// Stop stops one live agent through its supported path only (see
// decideStop). The decision is re-made on a fresh snapshot, never trusted
// from the browser.
func (c *Collector) Stop(ctx context.Context, sessionID string) (string, error) {
	c.invalidate()
	snap := c.Snapshot(ctx)
	var target *Session
	for i := range snap.Sessions {
		if snap.Sessions[i].SessionID == sessionID {
			target = &snap.Sessions[i]
		}
	}
	if target == nil || !target.Live {
		return "", ErrNotFound("no live session with that id")
	}
	switch target.StopVia {
	case "kcmdRestart":
		_, err := c.kcmd(ctx, "restart", target.inPane)
		return "kcmd restart " + target.inPane, err
	case "claudeStop":
		short := target.SessionID
		if len(short) > 8 {
			short = short[:8]
		}
		if !shortIDPattern.MatchString(short) {
			return "", ErrConflict("unexpected session id format")
		}
		for _, o := range snap.Sessions {
			if o.Live && o.SessionID != target.SessionID && strings.HasPrefix(o.SessionID, short) {
				return "", ErrConflict("short id " + short + " is ambiguous — stop it from a terminal")
			}
		}
		cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		out, err := c.Runner.Run(cctx, c.ClaudePath, "stop", short)
		if err != nil {
			return "", &APIError{502, "AIAGENT_STOP_FAILED", "claude stop failed: " + truncateRunes(strings.TrimSpace(string(out)), 300)}
		}
		return "claude stop " + short, nil
	}
	return "", ErrConflict(target.StopBlocked)
}

func truncateRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n]) + "…"
}

// RunIn runs a process in dir with stdin (never through a shell).
func (ExecRunner) RunIn(ctx context.Context, dir, stdin, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "TERM=dumb")
	cmd.Stdin = strings.NewReader(stdin)
	out, err := cmd.Output()
	if len(out) > 64<<10 {
		out = out[:64<<10]
	}
	return out, err
}

type dirRunner interface {
	RunIn(ctx context.Context, dir, stdin, name string, args ...string) ([]byte, error)
}

func (c *Collector) runIn(ctx context.Context, dir, stdin, name string, args ...string) (string, error) {
	r, ok := c.Runner.(dirRunner)
	if !ok {
		return "", errors.New("runner cannot run in a directory")
	}
	b, err := r.RunIn(ctx, dir, stdin, name, args...)
	return string(b), err
}
