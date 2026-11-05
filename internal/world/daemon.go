// internal/aiagent/daemon.go
//
// `claude agents --json --all` is Claude Code's supported scripting view of
// the same list its agent view shows (Needs input / Working / Completed). It
// also lists background sessions the daemon has parked (no process right
// now) that `claude attach <id>` reopens — invisible to the pid registry.
package world

import (
	"context"
	"encoding/json"
	"time"
)

// Agent-view states, as Claude Code reports them.
const (
	StatusNeedsInput = "needsInput" // agent view: "Needs input"
	StatusCompleted  = "completed"  // agent view: "Completed"
)

type daemonEntry struct {
	ID        string `json:"id"`
	SessionID string `json:"sessionId"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	State     string `json:"state"`
	Cwd       string `json:"cwd"`
	StartedAt int64  `json:"startedAt"`
}

// daemonTTL: `claude agents` costs ~150 ms (it starts a node process);
// agent-view state changes slowly, so reuse it across snapshots.
const daemonTTL = 10 * time.Second

func (c *Collector) daemonList(ctx context.Context) []daemonEntry {
	c.daemonMu.Lock()
	if c.daemonCache != nil && c.now().Sub(c.daemonAt) < daemonTTL {
		v := c.daemonCache
		c.daemonMu.Unlock()
		return v
	}
	c.daemonMu.Unlock()
	v := c.daemonFetch(ctx)
	c.daemonMu.Lock()
	c.daemonCache, c.daemonAt = v, c.now()
	if v == nil {
		c.daemonCache = []daemonEntry{}
	}
	c.daemonMu.Unlock()
	return v
}

func (c *Collector) daemonFetch(ctx context.Context) []daemonEntry {
	cctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	out, err := c.Runner.Run(cctx, c.ClaudePath, "agents", "--json", "--all")
	if err != nil {
		return nil
	}
	var list []daemonEntry
	if json.Unmarshal(out, &list) != nil {
		return nil
	}
	return list
}

// mergeDaemon applies agent-view state to known sessions and adds parked
// background sessions of the service user's home.
func (c *Collector) mergeDaemon(ctx context.Context, sessions []Session) []Session {
	list := c.daemonList(ctx)
	if len(list) == 0 {
		return sessions
	}
	home := c.serviceHome()
	index := map[string]int{}
	for i, s := range sessions {
		if s.RuntimeType == "claudeCode" {
			index[s.SessionID] = i
		}
	}
	for _, e := range list {
		if e.Kind != "background" || e.SessionID == "" {
			continue
		}
		i, ok := index[e.SessionID]
		if !ok {
			s := Session{
				SessionID:    e.SessionID,
				RuntimeType:  "claudeCode",
				ProviderID:   "anthropic",
				DisplayName:  e.Name,
				Owner:        "root",
				Cwd:          e.Cwd,
				RepoID:       repoID(e.Cwd),
				Live:         true,
				LiveEvidence: "claudeDaemon",
				ActivityType: ActivityIdle,
				Kind:         "bg",
				home:         home,
				Usage:        Usage{Source: "notCounted"},
			}
			if h := filepathBase(home); h != "" {
				s.Owner = h
			}
			if e.StartedAt > 0 {
				t := time.UnixMilli(e.StartedAt).UTC()
				s.StartedAt = &t
			}
			if tp := c.claudeTranscript(home, e.SessionID); tp != "" {
				c.applyClaudeTranscript(tp, &s, false)
			}
			sessions = append(sessions, s)
			i = len(sessions) - 1
			index[e.SessionID] = i
		}
		s := &sessions[i]
		s.Kind = "bg"
		if s.home == "" {
			s.home = home
		}
		if e.Name != "" {
			s.DisplayName = e.Name
		}
		s.AgentState = e.State
		s.Live = true
		if s.LiveEvidence == "none" {
			s.LiveEvidence = "claudeDaemon"
		}
		switch e.State {
		case "working":
			s.Status = StatusRunning
			if s.ActivityType == ActivityIdle {
				s.ActivityType = ActivityPlanning
			}
		case "blocked":
			s.Status, s.ActivityType = StatusNeedsInput, ActivityIdle
		case "done":
			s.Status, s.ActivityType = StatusCompleted, ActivityIdle
		}
	}
	return sessions
}
