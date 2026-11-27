// internal/world/askrelay.go
//
// A question asked by a background session (Claude Code's daemon job,
// kind=bg). The daemon draws the session's screen in its own pty, which no
// tmux pane shows, so the screen reader in the collector never saw the
// AskUserQuestion dialog: the agent view said "blocked", the web "รอคุณตอบ",
// and nothing could be picked (owner, 2026-11-28).
//
// Reading: the relay pane (`claude attach <id>` in aiagent-<id>, relay.go) is
// read like an agcmd pane when it is up; otherwise the dialog is the pending
// AskUserQuestion tool call in the transcript (askPrompt), shown only while the
// agent view says the job is blocked.
//
// Answering: the relay pane is opened if it is not up, and the keys go to it
// only once its screen shows the dialog; the ask driver then checks every key
// against the screen as for an agcmd pane.
package world

import (
	"context"
	"os"
	"strings"
	"time"
)

// relayPrompt fills in the question of a blocked background session.
func (c *Collector) relayPrompt(ctx context.Context, ss *Session) {
	if ss.Prompt != nil || ss.MessageVia != "relay" || ss.AgentState != "blocked" || ss.RuntimeType != "claudeCode" {
		return
	}
	var qs []AskQuestion
	if tp := c.sessionTranscript(ss); tp != "" {
		if fi, err := os.Stat(tp); err == nil {
			qs = pendingAsk(readTail(tp, fi.Size(), askTailBytes))
		}
	}
	name := relayName(shortID(ss.SessionID))
	if _, err := c.tmux(ctx, "has-session", "-t", "="+name); err == nil {
		if dead, _ := c.relayDead(ctx, name); !dead {
			screen, _ := c.tmux(ctx, "capture-pane", "-e", "-p", "-t", "="+name+":")
			if onDialog(sgrRE.ReplaceAllString(screen, "")) {
				if p := readPrompt(screen); p != nil {
					if p.Ask != nil {
						p.Ask.withQuestions(qs)
					}
					ss.Prompt = p
					return
				}
			}
		}
	}
	ss.Prompt = askPrompt(qs)
}

func (c *Collector) sessionTranscript(ss *Session) string {
	if ss.sourcePath != "" {
		return ss.sourcePath
	}
	return c.claudeTranscript(ss.home, ss.SessionID)
}

// relayForAnswer brings up the relay pane of a background session and waits
// until it shows a question. It returns the pane target; the caller holds the
// relay's send lock.
func (c *Collector) relayForAnswer(ctx context.Context, ss *Session, name string) (string, error) {
	pane := "=" + name + ":"
	if c.heldElsewhere(ctx, ss, name) {
		return "", &APIError{409, "AIAGENT_RELAY_HELD", "session นี้เปิดอยู่ใน Claude Code ที่เทอร์มินัลหรือ VS Code อยู่แล้ว ให้ตอบคำถามจากที่นั่น"}
	}
	dir := c.resumeDir(ss)
	if dead, _ := c.relayDead(ctx, name); dead {
		_, _ = c.tmux(ctx, "kill-session", "-t", "="+name)
	}
	if c.relayOnTrust(ctx, name) {
		c.trustFolder(dir)
		_, _ = c.tmux(ctx, "kill-session", "-t", "="+name)
	}
	if _, err := c.tmux(ctx, "has-session", "-t", "="+name); err != nil {
		// the same attach client relaySend opens: it never owns the session
		argv := []string{"new-session", "-d", "-s", name, "-x", "220", "-y", "50"}
		if dir != "" {
			argv = append(argv, "-c", dir)
			c.trustFolder(dir)
		}
		argv = append(argv, c.ClaudeBin(), "attach", shortID(ss.SessionID))
		if _, err := c.tmux(ctx, argv...); err != nil {
			return "", &APIError{502, "AIAGENT_RELAY_FAILED", "เปิด relay ไม่สำเร็จ: " + err.Error()}
		}
		_, _ = c.tmux(ctx, "set-option", "-w", "-t", pane, "remain-on-exit", "on")
		_, _ = c.tmux(ctx, "set-option", "-w", "-t", pane, "window-size", "manual")
		_, _ = c.tmux(ctx, "resize-window", "-t", pane, "-x", "220", "-y", "50")
	}
	for i := 0; i < 40; i++ {
		if dead, why := c.relayDead(ctx, name); dead {
			if why == "" {
				why = "จบการทำงานโดยไม่บอกเหตุผล"
			}
			return "", &APIError{502, "AIAGENT_RELAY_FAILED", "CLI ของ agent ไม่ยอมให้ต่อ: " + why}
		}
		screen, _ := c.tmux(ctx, "capture-pane", "-e", "-p", "-t", pane)
		plain := sgrRE.ReplaceAllString(screen, "")
		switch {
		case strings.Contains(plain, trustDialog):
			// its own Enter to confirm would pass for the question
			return "", ErrConflict(trustReason(dir))
		case strings.Contains(plain, agentViewBox):
			return "", ErrConflict("relay อยู่ที่หน้ารายการ agent ไม่ใช่ในบทสนทนา ให้ลองตอบใหม่อีกครั้ง")
		case onDialog(plain):
			return pane, nil
		}
		c.sleep(400 * time.Millisecond)
	}
	return "", ErrConflict("agent ไม่ได้แสดงคำถามแล้ว")
}
