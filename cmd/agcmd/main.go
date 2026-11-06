// Command agcmd talks to the agent CLIs running in tmux.
//
//	agcmd ws list                        what is configured, and what is alive
//	agcmd dispatch <ws> <message>        send a message to that workspace's agent
//	agcmd eng <task> <ws>                create a tracked run and send the task
//	agcmd eng status [<runId>]           list runs, or show one
//	agcmd eng answer <runId> <answer>    answer a run waiting on a decision
//	agcmd eng approve|abort|archive <runId>
//	agcmd eng note <runId> <text>        append a line to the run log
//	agcmd restart <ws>                   interrupt what the agent is doing
//
// AI Agent World shells out to exactly these subcommands, so the two stay in
// step; you can also run them by hand, which is the quickest way to find out
// whether a problem is in the service or in tmux.
//
// Three rules it will not bend, because the web can reach it:
//
//   - No shell. Every tmux call is a fixed argv.
//   - Only workspaces in the config file resolve. An unknown name is refused
//     and never reaches a command line.
//   - Only a pane actually running an agent CLI is written to. Typing into a
//     bare shell would run the operator's text as a command.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/hotkhwan/aiagent/internal/world"
)

const usage = `agcmd — talk to the agent CLIs running in tmux

  agcmd ws list                           configured workspaces and their state
  agcmd dispatch <ws> <message>           send a message to that workspace's agent
  agcmd eng <task> <ws>                   create a tracked run and send the task
  agcmd eng status [<runId>]              list runs, or show one
  agcmd eng answer <runId> <answer>       answer a run waiting on a decision
  agcmd eng approve <runId>               mark a waiting run approved
  agcmd eng abort <runId>                 stop a run and mark it aborted
  agcmd eng archive <runId>               hide a finished run from the board
  agcmd eng note <runId> <text>           append a line to the run log
  agcmd restart <ws>                      interrupt what the agent is doing

Environment:
  AGCMD_WORKSPACES   workspace config (default <state-dir>/workspaces.json)
  AGCMD_RUNS         run records     (default <state-dir>/runs)
  AGCMD_TMUX         tmux binary     (default: tmux on PATH)

A workspace's tmux session must have a window named "agent" whose active pane
is running claude, codex or node. agcmd refuses to type into a bare shell.
`

// maxTextRunes matches the service's own limit, so text that the web accepts
// is text agcmd accepts.
const maxTextRunes = 4000

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "ws":
		err = cmdWS(os.Args[2:])
	case "dispatch":
		err = cmdDispatch(os.Args[2:])
	case "eng":
		err = cmdEng(os.Args[2:])
	case "restart":
		err = cmdRestart(os.Args[2:])
	case "-h", "--help", "help":
		fmt.Print(usage)
		return
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", os.Args[1], usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "agcmd: "+err.Error())
		os.Exit(1)
	}
}

// ---------- configuration ----------

func workspacesPath() string {
	if v := os.Getenv("AGCMD_WORKSPACES"); v != "" {
		return v
	}
	return world.DefaultWorkspacesPath()
}

// RunsDir is where run records live. The service reads the same directory.
func runsDir() string {
	if v := os.Getenv("AGCMD_RUNS"); v != "" {
		return v
	}
	return filepath.Join(world.DefaultStateDir(), "runs")
}

func tmuxBin() string {
	if v := os.Getenv("AGCMD_TMUX"); v != "" {
		return v
	}
	return "tmux"
}

// resolve turns a name from the command line into a configured workspace, or
// refuses it. Nothing else in this program accepts a workspace name.
func resolve(name string) (world.WorkspaceInfo, error) {
	list, err := world.LoadWorkspaceList(workspacesPath())
	if err != nil {
		return world.WorkspaceInfo{}, err
	}
	if len(list) == 0 {
		return world.WorkspaceInfo{}, fmt.Errorf("no workspaces configured in %s — nothing can be dispatched", workspacesPath())
	}
	for _, w := range list {
		if w.Name == name {
			return w, nil
		}
	}
	names := make([]string, 0, len(list))
	for _, w := range list {
		names = append(names, w.Name)
	}
	return world.WorkspaceInfo{}, fmt.Errorf("unknown workspace %q (configured: %s)", name, strings.Join(names, ", "))
}

// ---------- tmux ----------

func tmux(args ...string) (string, error) {
	cmd := exec.Command(tmuxBin(), args...)
	cmd.Env = append(os.Environ(), "TERM=dumb")
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// shellCommands are what a pane runs when it is NOT an agent. Typing into one
// of these would execute the text as a command.
var shellCommands = map[string]bool{
	"bash": true, "sh": true, "zsh": true, "fish": true, "dash": true,
	"login": true, "tmux": true, "": true,
}

// agentPane returns the target for a workspace's agent window and what it is
// running, refusing anything that is not an agent CLI.
func agentPane(w world.WorkspaceInfo) (target, running string, err error) {
	out, err := tmux("list-panes", "-t", w.TmuxSession+":agent", "-F", "#{pane_active}\t#{pane_current_command}")
	if err != nil {
		return "", "", fmt.Errorf("workspace %q: no tmux session %q with an \"agent\" window (%s)", w.Name, w.TmuxSession, out)
	}
	for _, line := range strings.Split(out, "\n") {
		f := strings.SplitN(strings.TrimSpace(line), "\t", 2)
		if len(f) != 2 || f[0] != "1" {
			continue
		}
		running = f[1]
		if shellCommands[running] {
			return "", running, fmt.Errorf("workspace %q: the agent pane is running %q, not an agent CLI — refusing to type into a shell", w.Name, running)
		}
		return w.TmuxSession + ":agent", running, nil
	}
	return "", "", fmt.Errorf("workspace %q: no active pane in %s:agent", w.Name, w.TmuxSession)
}

// send pastes text into a pane and submits it.
//
// It goes through a tmux buffer and a bracketed paste rather than send-keys
// with the literal text, because a literal newline IS Enter to send-keys: a
// two-line message would submit the first line on its own and leave the
// second half typed but unsent. Bracketed paste keeps the whole message in
// the agent's input box, which is also what happens when a human pastes.
func send(target, text string) error {
	buf := "agcmd-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	cmd := exec.Command(tmuxBin(), "load-buffer", "-b", buf, "-")
	cmd.Stdin = bytes.NewReader([]byte(text))
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("tmux load-buffer: %s", strings.TrimSpace(string(out)))
	}
	if out, err := tmux("paste-buffer", "-b", buf, "-d", "-p", "-t", target); err != nil {
		_, _ = tmux("delete-buffer", "-b", buf)
		return fmt.Errorf("tmux paste-buffer: %s", out)
	}
	// A short pause: the CLI has to process the paste before Enter means
	// "submit this" rather than arriving mid-paste.
	time.Sleep(150 * time.Millisecond)
	if out, err := tmux("send-keys", "-t", target, "Enter"); err != nil {
		return fmt.Errorf("tmux send-keys Enter: %s", out)
	}
	return nil
}

// cleanText mirrors the service's validation: valid UTF-8, CRLF normalised,
// control characters dropped, trimmed, length-capped, and never starting with
// '-' so it cannot be read as a flag downstream.
func cleanText(s string) (string, error) {
	if !utf8.ValidString(s) {
		return "", errors.New("text must be valid UTF-8")
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
	s = strings.TrimSpace(s)
	if n := utf8.RuneCountInString(s); n == 0 {
		return "", errors.New("text is required")
	} else if n > maxTextRunes {
		return "", fmt.Errorf("text is %d characters; the limit is %d", n, maxTextRunes)
	}
	if strings.HasPrefix(s, "-") {
		return "", errors.New("text must not start with '-'")
	}
	return s, nil
}

// ---------- run records ----------

// run is one tracked piece of work. The field names are what AI Agent World
// reads, so they stay as they are.
type run struct {
	ID        string    `json:"id"`
	Task      string    `json:"task"`
	Workspace string    `json:"workspace"`
	Session   string    `json:"session"`
	Status    string    `json:"status"`
	CreatedAt string    `json:"created_at"`
	UpdatedAt string    `json:"updated_at"`
	Decision  *decision `json:"decision"`
}

type decision struct {
	Question string   `json:"question"`
	Options  []string `json:"options"`
}

const (
	statusRunning  = "RUNNING"
	statusWaiting  = "WAITING_DECISION"
	statusDone     = "DONE"
	statusAborted  = "ABORTED"
	statusArchived = "ARCHIVED"
)

func runPath(id string) string { return filepath.Join(runsDir(), id+".json") }
func logPath(id string) string { return filepath.Join(runsDir(), id+".log") }
func nowStamp() string         { return time.Now().UTC().Format(time.RFC3339) }
func validID(id string) bool   { return runIDPattern.MatchString(id) }
func runIDError(id string) error {
	return fmt.Errorf("%q is not a run id (expected ENG-YYYYMMDD-NNN)", id)
}

func loadRun(id string) (run, error) {
	var r run
	if !validID(id) {
		return r, runIDError(id)
	}
	b, err := os.ReadFile(runPath(id))
	if err != nil {
		if os.IsNotExist(err) {
			return r, fmt.Errorf("no run %s", id)
		}
		return r, err
	}
	return r, json.Unmarshal(b, &r)
}

// saveRun writes atomically: the service polls this directory, and a
// half-written file would be read as a corrupt run.
func saveRun(r run) error {
	if err := os.MkdirAll(runsDir(), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(runsDir(), ".run-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), runPath(r.ID))
}

func appendLog(id, line string) error {
	if err := os.MkdirAll(runsDir(), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(logPath(id), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = fmt.Fprintf(f, "%s  %s\n", time.Now().Format("15:04:05"), line)
	return err
}

// nextRunID allocates ENG-<date>-NNN, counting only today's records so the
// numbers stay short and readable.
func nextRunID() (string, error) {
	day := time.Now().Format("20060102")
	prefix := "ENG-" + day + "-"
	files, _ := filepath.Glob(filepath.Join(runsDir(), prefix+"*.json"))
	high := 0
	for _, p := range files {
		n, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(filepath.Base(p), prefix), ".json"))
		if err == nil && n > high {
			high = n
		}
	}
	return fmt.Sprintf("%s%03d", prefix, high+1), nil
}

// ---------- commands ----------

func cmdWS(args []string) error {
	if len(args) == 0 || args[0] != "list" {
		return errors.New("usage: agcmd ws list")
	}
	list, err := world.LoadWorkspaceList(workspacesPath())
	if err != nil {
		return err
	}
	if len(list) == 0 {
		fmt.Printf("no workspaces configured in %s\n\n", workspacesPath())
		fmt.Print(`Create it, with one entry per checkout an agent works in:

  {
    "workspaces": [
      {"name": "web", "tmuxSession": "web", "dir": "/home/you/src/web"}
    ]
  }

The tmux session needs a window named "agent" running claude or codex.
`)
		return nil
	}
	fmt.Printf("%-14s %-18s %-10s %s\n", "NAME", "TMUX SESSION", "AGENT", "DIR")
	for _, w := range list {
		state := "—"
		if _, running, err := agentPane(w); err == nil {
			state = running
		} else if running != "" {
			state = running + " (not an agent)"
		}
		dir := w.Dir
		if _, err := os.Stat(dir); err != nil {
			dir += "  (missing)"
		}
		fmt.Printf("%-14s %-18s %-10s %s\n", w.Name, w.TmuxSession, state, dir)
	}
	return nil
}

func cmdDispatch(args []string) error {
	if len(args) != 2 {
		return errors.New("usage: agcmd dispatch <workspace> <message>")
	}
	w, err := resolve(args[0])
	if err != nil {
		return err
	}
	msg, err := cleanText(args[1])
	if err != nil {
		return err
	}
	target, _, err := agentPane(w)
	if err != nil {
		return err
	}
	return send(target, msg)
}

func cmdRestart(args []string) error {
	if len(args) != 1 {
		return errors.New("usage: agcmd restart <workspace>")
	}
	w, err := resolve(args[0])
	if err != nil {
		return err
	}
	target, _, err := agentPane(w)
	if err != nil {
		return err
	}
	// Escape interrupts the current turn in both Claude Code and Codex. The
	// session, its history and its context survive — which is the point. A
	// kill would lose the conversation, and this is reachable from the web.
	if out, err := tmux("send-keys", "-t", target, "Escape"); err != nil {
		return fmt.Errorf("tmux send-keys Escape: %s", out)
	}
	fmt.Printf("interrupted the agent in %s\n", w.Name)
	return nil
}

func cmdEng(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: agcmd eng <task> <workspace> | agcmd eng <subcommand> ...")
	}
	switch args[0] {
	case "status":
		return engStatus(args[1:])
	case "answer":
		return engAnswer(args[1:])
	case "approve":
		return engFinish(args[1:], statusDone, "approved")
	case "abort":
		return engFinish(args[1:], statusAborted, "aborted")
	case "archive":
		return engFinish(args[1:], statusArchived, "archived")
	case "note":
		return engNote(args[1:])
	}
	// Not a subcommand, so: agcmd eng <task> <workspace>
	if len(args) != 2 {
		return errors.New("usage: agcmd eng <task> <workspace>")
	}
	return engCreate(args[0], args[1])
}

func engCreate(task, workspace string) error {
	w, err := resolve(workspace)
	if err != nil {
		return err
	}
	task, err = cleanText(task)
	if err != nil {
		return err
	}
	target, _, err := agentPane(w)
	if err != nil {
		return err
	}
	id, err := nextRunID()
	if err != nil {
		return err
	}
	now := nowStamp()
	r := run{ID: id, Task: task, Workspace: w.Name, Session: w.TmuxSession, Status: statusRunning, CreatedAt: now, UpdatedAt: now}
	// The record is written BEFORE the message is sent. If sending fails the
	// run is still on the board as something that needs looking at, rather
	// than work that happened with no trace.
	if err := saveRun(r); err != nil {
		return err
	}
	_ = appendLog(id, "created in workspace "+w.Name+": "+firstLine(task))
	if err := send(target, task); err != nil {
		r.Status, r.UpdatedAt = statusAborted, nowStamp()
		_ = saveRun(r)
		_ = appendLog(id, "could not send: "+err.Error())
		return err
	}
	_ = appendLog(id, "sent to "+target)
	// This line is the contract with AI Agent World: it reads the run id back
	// out of stdout with the pattern `(ENG-\d{8}-\d{3,})\s+RUNNING`.
	fmt.Printf("%s  %s  %s\n", id, statusRunning, w.Name)
	return nil
}

func engStatus(args []string) error {
	if len(args) == 1 {
		r, err := loadRun(args[0])
		if err != nil {
			return err
		}
		b, _ := json.MarshalIndent(r, "", "  ")
		fmt.Println(string(b))
		return nil
	}
	files, _ := filepath.Glob(filepath.Join(runsDir(), "ENG-*.json"))
	sort.Strings(files)
	if len(files) == 0 {
		fmt.Printf("no runs in %s\n", runsDir())
		return nil
	}
	fmt.Printf("%-20s %-16s %-12s %s\n", "ID", "STATUS", "WORKSPACE", "TASK")
	for _, p := range files {
		var r run
		b, err := os.ReadFile(p)
		if err != nil || json.Unmarshal(b, &r) != nil {
			continue
		}
		fmt.Printf("%-20s %-16s %-12s %s\n", r.ID, r.Status, r.Workspace, firstLine(r.Task))
	}
	return nil
}

func engAnswer(args []string) error {
	if len(args) != 2 {
		return errors.New("usage: agcmd eng answer <runId> <answer>")
	}
	r, err := loadRun(args[0])
	if err != nil {
		return err
	}
	answer, err := cleanText(args[1])
	if err != nil {
		return err
	}
	w, err := resolve(r.Workspace)
	if err != nil {
		return err
	}
	target, _, err := agentPane(w)
	if err != nil {
		return err
	}
	if err := send(target, answer); err != nil {
		return err
	}
	r.Status, r.Decision, r.UpdatedAt = statusRunning, nil, nowStamp()
	_ = appendLog(r.ID, "answered: "+firstLine(answer))
	return saveRun(r)
}

func engFinish(args []string, status, verb string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: agcmd eng %s <runId>", verb)
	}
	r, err := loadRun(args[0])
	if err != nil {
		return err
	}
	if status == statusAborted {
		// Aborting should actually stop the agent, not just relabel the record.
		if w, err := resolve(r.Workspace); err == nil {
			if target, _, err := agentPane(w); err == nil {
				if _, err := tmux("send-keys", "-t", target, "Escape"); err != nil {
					_ = appendLog(r.ID, "could not interrupt the agent: "+err.Error())
				}
			}
		}
	}
	r.Status, r.Decision, r.UpdatedAt = status, nil, nowStamp()
	_ = appendLog(r.ID, verb)
	if err := saveRun(r); err != nil {
		return err
	}
	fmt.Printf("%s  %s\n", r.ID, r.Status)
	return nil
}

func engNote(args []string) error {
	if len(args) != 2 {
		return errors.New("usage: agcmd eng note <runId> <text>")
	}
	if !validID(args[0]) {
		return runIDError(args[0])
	}
	note, err := cleanText(args[1])
	if err != nil {
		return err
	}
	return appendLog(args[0], note)
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i] + " …"
	}
	if len([]rune(s)) > 70 {
		s = string([]rune(s)[:70]) + "…"
	}
	return s
}

// runIDPattern is the id shape AI Agent World validates against before it
// will read a run file or a log, so agcmd must mint ids that match it.
var runIDPattern = regexp.MustCompile(`^ENG-[0-9]{8}-[0-9]{3,}$`)
