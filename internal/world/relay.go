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
		return "relay " + relayName(short), c.relaySend(ctx, short, message)
	}
	return "", ErrConflict(target.MessageBlocked)
}

// ModelAliases are the short names; full ids (claude-haiku-5-5) are accepted
// too, nothing else (claudeModelRE).
var ModelAliases = []string{"opus", "sonnet", "haiku", "fable"}

// ClaudeModels is the menu offered by the web (id, label).
var ClaudeModels = [][2]string{
	{"claude-fable-5-1", "Fable 5.1"}, {"claude-opus-5-5", "Opus 5.5"}, {"claude-sonnet-5-5", "Sonnet 5.5"}, {"claude-haiku-5-5", "Haiku 5.5"},
	{"opus", "Opus (ล่าสุด)"}, {"sonnet", "Sonnet (ล่าสุด)"}, {"haiku", "Haiku (ล่าสุด)"},
}

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

func (c *Collector) tmux(ctx context.Context, args ...string) (string, error) {
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := c.Runner.Run(cctx, c.TmuxPath, args...)
	return string(out), err
}

func (c *Collector) relaySend(ctx context.Context, short, message string) error {
	name := relayName(short)
	if _, err := c.tmux(ctx, "has-session", "-t", "="+name); err != nil {
		if _, err := c.tmux(ctx, "new-session", "-d", "-s", name, "-x", "220", "-y", "50", c.ClaudePath, "attach", short); err != nil {
			return &APIError{502, "AIAGENT_RELAY_FAILED", "could not start relay: " + err.Error()}
		}
	}
	// pin the pane size: with tmux's default window-size a smaller client
	// (or a resize) can shrink the pane until Claude's input box is no
	// longer drawn, and the relay then never sees the prompt
	// window-size is a window option: target the session's window ("=name:"),
	// a bare "=name" is rejected as "no such window" and the size reverts
	_, _ = c.tmux(ctx, "set-option", "-w", "-t", "="+name+":", "window-size", "manual")
	_, _ = c.tmux(ctx, "resize-window", "-t", "="+name+":", "-x", "220", "-y", "50")
	// wait until the pane runs the attach client and its prompt is drawn
	//
	// capture-pane needs "=name:" like the window options above, NOT "=name":
	// with the bare form it answers `can't find pane` on every attempt, so the
	// screen was always empty, the prompt was never found, and every relayed
	// message failed with RELAY_NOT_READY after the full 16-second wait. The
	// window-size comment above records the same trap one command earlier.
	ready := false
	for i := 0; i < 40 && !ready; i++ {
		cmd, err := c.tmux(ctx, "list-panes", "-t", "="+name, "-F", "#{pane_current_command}")
		if err == nil && agentReady(strings.TrimSpace(cmd)) {
			screen, _ := c.tmux(ctx, "capture-pane", "-p", "-t", "="+name+":")
			ready = strings.Contains(screen, "❯")
		}
		if !ready {
			c.sleep(400 * time.Millisecond)
		}
	}
	if !ready {
		return &APIError{504, "AIAGENT_RELAY_NOT_READY",
			"the session did not become ready to receive text — it is probably still working; try again when it is idle"}
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
