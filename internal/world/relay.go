// internal/aiagent/relay.go
//
// Messaging a session directly (not a agcmd workspace):
//
//	agcmd   — the session is the CLI in an agcmd tmux agent pane, or attached
//	          there with `claude attach`: `agcmd dispatch <workspace>`.
//	relay   — an own background Claude session (kind=bg) that no agcmd pane
//	          attaches: this service keeps a private tmux session `aiagent-<id>` whose
//	          ONLY process is `claude attach <id>` (no shell; if attach exits,
//	          the tmux session ends), and types into it. Verified on a dev box:
//	          the attached session receives the text as a user turn, and other
//	          attached clients keep working.
//	(none)  — interactive terminal / VS Code sessions: there is no supported
//	          way to type into someone else's terminal, so they stay read-only.
package world

import (
	"context"
	"path/filepath"
	"strings"
	"time"
)

// relayName is the private tmux session used to reach a background session.
func relayName(short string) string { return "aiagent-" + short }

func (c *Collector) decideMessage(ss *Session) {
	if !ss.Live {
		return
	}
	switch {
	case ss.DispatchVia != "":
		ss.MessageVia = "agcmd"
	case ss.RuntimeType == "claudeCode" && ss.Kind == "bg" && ss.home == c.serviceHome() && shortIDPattern.MatchString(shortID(ss.SessionID)):
		ss.MessageVia = "relay"
	case ss.RuntimeType == "claudeCode" && ss.Kind == "bg":
		ss.MessageBlocked = "background session ของผู้ใช้ " + ss.Owner + " — บริการนี้ส่งได้เฉพาะ session ของตัวเอง"
	default:
		ss.MessageBlocked = "เปิดอยู่ในเทอร์มินัล/VS Code (interactive) — ไม่มีวิธีพิมพ์เข้าเทอร์มินัลของคนอื่นอย่างปลอดภัย จึงดูได้อย่างเดียว"
	}
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func (c *Collector) sleep(d time.Duration) {
	if c.Sleep != nil {
		c.Sleep(d)
		return
	}
	time.Sleep(d)
}

// SendToSession types a message into one specific live session.
func (c *Collector) SendToSession(ctx context.Context, id, message string) (string, error) {
	message, err := cleanText(message)
	if err != nil {
		return "", err
	}
	c.invalidate()
	snap := c.Snapshot(ctx)
	var target *Session
	for i := range snap.Sessions {
		if snap.Sessions[i].SessionID == id {
			target = &snap.Sessions[i]
		}
	}
	if target == nil || !target.Live {
		return "", ErrNotFound("no live session with that id")
	}
	switch target.MessageVia {
	case "agcmd":
		return "agcmd dispatch " + target.DispatchVia, c.Message(ctx, target.DispatchVia, message)
	case "relay":
		short := shortID(target.SessionID)
		for _, o := range snap.Sessions {
			if o.Live && o.SessionID != target.SessionID && strings.HasPrefix(o.SessionID, short) {
				return "", ErrConflict("short id " + short + " is ambiguous")
			}
		}
		return "relay " + relayName(short), c.relaySend(ctx, target, message)
	}
	return "", ErrConflict(target.MessageBlocked)
}

// ModelAliases are the short names; full ids (claude-haiku-5-5) are accepted
// too, nothing else (claudeModelRE).
var ModelAliases = []string{"opus", "sonnet", "haiku", "fable"}

// SetModel switches a Claude Code session's model by typing the /model
// command through the session's normal channel (agcmd or relay). Only the
// fixed aliases are accepted, so this cannot carry any other command.
func (c *Collector) SetModel(ctx context.Context, id, alias string) (string, error) {
	if !claudeModelRE.MatchString(alias) {
		return "", &APIError{400, "AIAGENT_BAD_MODEL", "model must be one of " + strings.Join(ModelAliases, ", ") + " or a full Claude model id (claude-haiku-5-5)"}
	}
	snap := c.Snapshot(ctx)
	for _, s := range snap.Sessions {
		if s.SessionID == id && s.RuntimeType != "claudeCode" {
			return "", ErrConflict("model switching from the web is supported for Claude Code sessions only")
		}
	}
	return c.SendToSession(ctx, id, "/model "+alias)
}

// RenameInCLI asks the session to rename itself, by typing /rename down the
// same channel a message goes down.
//
// There is no `claude rename` on the command line — `/rename` inside a running
// session is the only supported way to change the name Claude Code holds — so
// this is the only route that leaves the CLI and the web agreeing. A local
// alias can only ever disagree with one of them.
//
// The name is sanitised by cleanSessionName before it arrives: one line, no
// control characters, so it cannot carry a second command after the first.
func (c *Collector) RenameInCLI(ctx context.Context, id, name string) (string, error) {
	if name == "" {
		return "", ErrBadRequest("a name is required to rename in the CLI")
	}
	snap := c.Snapshot(ctx)
	for _, s := range snap.Sessions {
		if s.SessionID == id && s.RuntimeType != "claudeCode" {
			return "", ErrConflict("renaming through the CLI is supported for Claude Code sessions only")
		}
	}
	return c.SendToSession(ctx, id, "/rename "+name)
}

// ResumeSession starts a Claude Code session that is not running, on its own
// conversation in its own folder, and types nothing into it. The state says
// whether it was started now or was already running.
func (c *Collector) ResumeSession(ctx context.Context, id string) (string, error) {
	c.invalidate()
	snap := c.Snapshot(ctx)
	var target *Session
	for i := range snap.Sessions {
		if snap.Sessions[i].SessionID == id {
			target = &snap.Sessions[i]
		}
	}
	if target == nil {
		return "", ErrNotFound("no session with that id")
	}
	if target.Live {
		return "running", nil
	}
	if target.RuntimeType != "claudeCode" || target.home != c.serviceHome() {
		return "", ErrConflict("resume works for this machine's Claude Code sessions only")
	}
	if target.Cwd == "" {
		return "", ErrConflict("the session has no folder to resume in")
	}
	short := shortID(target.SessionID)
	for _, o := range snap.Sessions {
		if o.Live && o.SessionID != target.SessionID && strings.HasPrefix(o.SessionID, short) {
			return "", ErrConflict("short id " + short + " is ambiguous")
		}
	}
	name := relayName(short)
	defer c.lockSend("relay:" + name)()
	if dead, _ := c.relayDead(ctx, name); dead {
		_, _ = c.tmux(ctx, "kill-session", "-t", "="+name)
	}
	if _, err := c.tmux(ctx, "has-session", "-t", "="+name); err == nil {
		return "running", nil
	}
	argv := []string{"new-session", "-d", "-s", name, "-x", "220", "-y", "50", "-c", c.resumeDir(target), c.ClaudePath, "--resume", target.SessionID}
	if _, err := c.tmux(ctx, argv...); err != nil {
		return "", &APIError{502, "AIAGENT_RESUME_FAILED", "could not start the session: " + err.Error()}
	}
	_, _ = c.tmux(ctx, "set-option", "-w", "-t", "="+name+":", "remain-on-exit", "on")
	_, _ = c.tmux(ctx, "set-option", "-w", "-t", "="+name+":", "window-size", "manual")
	_, _ = c.tmux(ctx, "resize-window", "-t", "="+name+":", "-x", "220", "-y", "50")
	if ready, why := c.relayWait(ctx, name); !ready && why != "" {
		return "", &APIError{502, "AIAGENT_RESUME_FAILED", "claude --resume stopped: " + why}
	}
	return "started", nil
}

// resumeDir is where --resume must run: the folder the transcript is filed
// under. An agent that moved into a worktree reports that worktree as its cwd,
// and --resume run there finds no conversation and exits at once.
func (c *Collector) resumeDir(ss *Session) string {
	home := ss.home
	if home == "" {
		home = c.serviceHome()
	}
	m, _ := filepath.Glob(filepath.Join(home, ".claude", "projects", "*", ss.SessionID+".jsonl"))
	if len(m) == 0 {
		return ss.Cwd
	}
	slug := filepath.Base(filepath.Dir(m[0]))
	for d := ss.Cwd; d != "" && d != "."; d = filepath.Dir(d) {
		if projectSlug(d) == slug {
			return d
		}
		if d == "/" {
			break
		}
	}
	return ss.Cwd
}

// projectSlug is the name Claude Code files a folder's transcripts under.
func projectSlug(dir string) string {
	b := []byte(dir)
	for i, ch := range b {
		if !(ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9') {
			b[i] = '-'
		}
	}
	return string(b)
}

func (c *Collector) tmux(ctx context.Context, args ...string) (string, error) {
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := c.Runner.Run(cctx, c.TmuxPath, args...)
	return string(out), err
}

// attachReason pulls the CLI's own complaint off a dead relay pane, so the
// operator is told what the tool said rather than a guess about it.
func attachReason(screen string) string {
	for _, l := range strings.Split(screen, "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "Pane is dead") {
			continue
		}
		return truncateRunes(Redact(l), 160)
	}
	return "it exited without saying why"
}

// relayDead reports whether the relay's pane has exited, and what it last said.
func (c *Collector) relayDead(ctx context.Context, name string) (bool, string) {
	out, err := c.tmux(ctx, "list-panes", "-t", "="+name, "-F", "#{pane_dead}")
	if err != nil {
		return false, ""
	}
	f := strings.Fields(strings.TrimSpace(out))
	if len(f) == 0 || f[0] != "1" {
		return false, ""
	}
	screen, _ := c.tmux(ctx, "capture-pane", "-p", "-J", "-t", "="+name+":")
	return true, attachReason(screen)
}

// relayWait waits for the CLI's prompt. It returns ready, or the reason the
// pane died; an empty reason with ready false means it is alive but busy.
//
// capture-pane needs "=name:" like the window options, NOT "=name": with the
// bare form it answers `can't find pane` on every attempt, so the screen was
// always empty, the prompt was never found, and every relayed message failed
// with RELAY_NOT_READY after the full 16-second wait.
func (c *Collector) relayWait(ctx context.Context, name string) (bool, string) {
	for i := 0; i < 40; i++ {
		if dead, why := c.relayDead(ctx, name); dead {
			if why == "" {
				why = "it exited without saying why"
			}
			return false, why
		}
		cmd, err := c.tmux(ctx, "list-panes", "-t", "="+name, "-F", "#{pane_current_command}")
		if err == nil && agentReady(strings.TrimSpace(cmd)) {
			screen, _ := c.tmux(ctx, "capture-pane", "-p", "-t", "="+name+":")
			// The folder-trust dialog draws its own ❯ beside "No, exit": typed
			// text and Enter there would close the session, not message it.
			if strings.Contains(screen, "trust this folder") {
				return false, "Claude Code is asking whether to trust the session's folder; open the session once in a terminal and choose Yes"
			}
			if strings.Contains(screen, "❯") {
				return true, ""
			}
		}
		c.sleep(400 * time.Millisecond)
	}
	return false, ""
}

// relaySend types into the session, opening a relay for it if there is none.
//
// Two ways in, in this order. `claude attach <id>` joins a job the daemon is
// still running. A session that has finished has no job to join — attach
// answers "No job matching <id>" and exits — but the operator still wants to
// give it more work (owner, 2026-11-14: "complete แล้วจริง แต่ก็ต้องพิมพ์สั่ง
// เพิ่มได้"), and `claude --resume <id>` is how the CLI itself does that. So a
// refused attach is followed by a resume before anything is called a failure.
func (c *Collector) relaySend(ctx context.Context, ss *Session, message string) error {
	short := shortID(ss.SessionID)
	name := relayName(short)
	defer c.lockSend("relay:" + name)()
	// Two goes. remain-on-exit leaves a dead pane behind when an attach ends,
	// and a leftover one is not evidence that the CLI would refuse now — the
	// first pass replaces it, and only a fresh attach that also dies is
	// reported as a refusal. Without this every send after the first exit
	// failed instantly with "refused to attach".
	reason := ""
	first := 0
	if ss.AgentState == "done" {
		// A job the daemon lists as done has no process. Attaching to it neither
		// refuses nor draws a prompt, so the wait below took it for a busy agent
		// for ever. Resume it, and drop a relay still sitting on such an attach.
		first = 1
		if cmd, err := c.tmux(ctx, "list-panes", "-t", "="+name, "-F", "#{pane_start_command}"); err == nil && strings.Contains(cmd, " attach ") {
			_, _ = c.tmux(ctx, "kill-session", "-t", "="+name)
		}
	}
	for attempt := first; attempt < 2; attempt++ {
		if dead, why := c.relayDead(ctx, name); dead {
			if why != "" {
				reason = why
			}
			_, _ = c.tmux(ctx, "kill-session", "-t", "="+name)
		}
		if _, err := c.tmux(ctx, "has-session", "-t", "="+name); err != nil {
			argv := []string{"new-session", "-d", "-s", name, "-x", "220", "-y", "50"}
			if dir := c.resumeDir(ss); dir != "" {
				// --resume reads the transcript of the project it is run in.
				argv = append(argv, "-c", dir)
			}
			if attempt == 0 {
				argv = append(argv, c.ClaudePath, "attach", short)
			} else {
				argv = append(argv, c.ClaudePath, "--resume", ss.SessionID)
			}
			if _, err := c.tmux(ctx, argv...); err != nil {
				return &APIError{502, "AIAGENT_RELAY_FAILED", "could not start relay: " + err.Error()}
			}
			// Keep the pane after the client exits, so a refused attach can be
			// read back instead of vanishing. `claude attach` answers "No job
			// matching <id>" for a session its daemon does not list, and without
			// this the tmux session was simply gone a moment later — which the
			// wait below could only report as "not ready yet", i.e. as a busy
			// agent. That sent the operator off waiting for an agent to finish
			// something it was never doing.
			_, _ = c.tmux(ctx, "set-option", "-w", "-t", "="+name+":", "remain-on-exit", "on")
		}
		// pin the pane size: with tmux's default window-size a smaller client
		// (or a resize) can shrink the pane until Claude's input box is no
		// longer drawn, and the relay then never sees the prompt
		// window-size is a window option: target the session's window ("=name:"),
		// a bare "=name" is rejected as "no such window" and the size reverts
		_, _ = c.tmux(ctx, "set-option", "-w", "-t", "="+name+":", "window-size", "manual")
		_, _ = c.tmux(ctx, "resize-window", "-t", "="+name+":", "-x", "220", "-y", "50")
		ready, why := c.relayWait(ctx, name)
		if ready {
			reason = ""
			break
		}
		if why == "" {
			// Alive, just busy: the outbox holds the message and tries later.
			return &APIError{504, "AIAGENT_RELAY_NOT_READY",
				"the session did not become ready to receive text — it is probably still working; try again when it is idle"}
		}
		reason = why
	}
	if reason != "" {
		return &APIError{502, "AIAGENT_RELAY_FAILED", "the agent CLI refused to attach: " + reason}
	}
	// "=name:" again, not "=name": send-keys takes a pane target too.
	if _, err := c.tmux(ctx, "send-keys", "-t", "="+name+":", "-l", "--", message); err != nil {
		return &APIError{502, "AIAGENT_RELAY_FAILED", "send failed"}
	}
	c.sleep(400 * time.Millisecond)
	if _, err := c.tmux(ctx, "send-keys", "-t", "="+name+":", "Enter"); err != nil {
		return &APIError{502, "AIAGENT_RELAY_FAILED", "send failed"}
	}
	return nil
}
