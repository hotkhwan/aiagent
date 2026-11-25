// internal/world/agy.go
//
// Antigravity (agy) keeps its conversations in a private database, so the list
// shows the window it runs in: that it is there, whether it is on a question,
// and that work can be sent to it.
package world

import (
	"context"
	"strings"
)

// paneScreen reads what an agent's window shows; key is a tmuxKey.
func (c *Collector) paneScreen(ctx context.Context, key string) string {
	session, window, ok := strings.Cut(key, ":")
	if !ok {
		window = "agent"
	}
	screen, _ := c.tmux(ctx, "capture-pane", "-p", "-t", "="+session+":="+window)
	return screen
}

func (c *Collector) agySessions(ctx context.Context, tm tmuxState) []Session {
	var out []Session
	for _, w := range c.Workspaces {
		if w.provider != "gemini" || tm.agentCmd[w.session] != "agy" {
			continue
		}
		s := Session{
			SessionID: "agy-" + w.name, RuntimeType: "agyCli", ProviderID: "google",
			DisplayName: "Antigravity", Cwd: w.dir, RepoID: repoID(w.dir),
			Status: StatusIdle, ActivityType: ActivityIdle, Live: true, LiveEvidence: "process",
			Pid: tm.agentPid[w.session], Kind: "interactive", Usage: Usage{Source: "notCounted"},
		}
		screen := c.paneScreen(ctx, w.session)
		switch {
		case strings.Contains(screen, "not signed in"):
			s.Title = "ยังไม่ได้ login"
		case onDialog(screen):
			s.Status, s.Title = StatusNeedsInput, "รอคำตอบในหน้าต่าง tmux"
		case agyBusy(screen):
			s.Status = StatusRunning
		}
		out = append(out, s)
	}
	return out
}

// agyBusy reports a turn in progress: only the idle prompt shows its hint.
func agyBusy(screen string) bool {
	return strings.TrimSpace(screen) != "" && !strings.Contains(screen, "? for shortcuts")
}
