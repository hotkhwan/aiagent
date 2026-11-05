// internal/aiagent/subagents.go
//
// Sub-agents a Claude Code session spawned (Agent/Task tool). Claude Code
// keeps each one next to the parent transcript:
//
//	<project>/<sessionId>/subagents/agent-<id>.jsonl      its transcript
//	<project>/<sessionId>/subagents/agent-<id>.meta.json  type, description, finished
//
// They are shown as helpers, view-only: the web never types to a sub-agent.
package world

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// SubAgent is one helper of a session.
type SubAgent struct {
	ID           string     `json:"id"`
	Type         string     `json:"type"`
	Description  string     `json:"description"`
	Status       string     `json:"status"` // running | done | stale
	ActivityType string     `json:"activityType"`
	LastTool     string     `json:"lastTool,omitempty"`
	LastEventAt  *time.Time `json:"lastEventAt,omitempty"`
	// Task is the brief the parent gave it; Latest its most recent reply.
	Task   string `json:"task,omitempty"`
	Latest string `json:"latest,omitempty"`
}

const (
	subDoneLinger = 2 * time.Minute
	subStaleAfter = 15 * time.Minute
	subMax        = 12
)

func (c *Collector) subAgents(transcript, sessionID string) []SubAgent {
	dir := filepath.Join(strings.TrimSuffix(transcript, ".jsonl"), "subagents")
	metas, _ := filepath.Glob(filepath.Join(dir, "agent-*.meta.json"))
	now := c.now()
	var out []SubAgent
	for _, mp := range metas {
		b, err := os.ReadFile(mp)
		if err != nil {
			continue
		}
		var m struct {
			AgentType   string `json:"agentType"`
			Description string `json:"description"`
			Finished    bool   `json:"finished"`
		}
		if json.Unmarshal(b, &m) != nil {
			continue
		}
		jp := strings.TrimSuffix(mp, ".meta.json") + ".jsonl"
		fi, err := os.Stat(jp)
		if err != nil {
			continue
		}
		age := now.Sub(fi.ModTime())
		sa := SubAgent{
			ID:          strings.TrimPrefix(strings.TrimSuffix(filepath.Base(mp), ".meta.json"), "agent-"),
			Type:        m.AgentType,
			Description: truncateRunes(Redact(m.Description), 120),
			Status:      "running",
		}
		switch {
		case m.Finished && age > subDoneLinger:
			continue
		case m.Finished:
			sa.Status = "done"
		case age > subStaleAfter:
			continue
		}
		t := fi.ModTime().UTC()
		sa.LastEventAt = &t
		st := &transcriptState{size: fi.Size()}
		refreshClaudeTail(jp, st)
		sa.LastTool, sa.ActivityType = st.lastTool, st.activity
		if head := claudeTurns(readHead(jp, 256<<10), true); len(head) > 0 && head[0].Role == "user" {
			sa.Task = truncateRunes(head[0].Text, 400)
		}
		tail := claudeTurns(readTail(jp, fi.Size(), 256<<10), true)
		for i := len(tail) - 1; i >= 0; i-- {
			if tail[i].Role == "assistant" {
				sa.Latest = truncateRunes(tail[i].Text, 280)
				break
			}
		}
		if sa.ActivityType == "" {
			sa.ActivityType = ActivityResearch
		}
		out = append(out, sa)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	if len(out) > subMax {
		out = out[:subMax]
	}
	return out
}

// readHead returns the complete lines in the first n bytes of a file.
func readHead(path string, n int64) [][]byte {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	buf := make([]byte, n)
	k, _ := io.ReadFull(f, buf)
	buf = buf[:k]
	if int64(k) == n {
		if i := bytes.LastIndexByte(buf, '\n'); i >= 0 {
			buf = buf[:i]
		}
	}
	return bytes.Split(buf, []byte{'\n'})
}

// JobDoing is the latest status line of a background job.
type JobDoing struct {
	State  string    `json:"state"`
	Detail string    `json:"detail"`
	At     time.Time `json:"at"`
}

const jobDoingFresh = 20 * time.Minute

func (c *Collector) jobDoing(s *Session) *JobDoing {
	if s.Kind != "bg" || s.home == "" || len(s.SessionID) < 8 {
		return nil
	}
	path := filepath.Join(s.home, ".claude", "jobs", shortID(s.SessionID), "timeline.jsonl")
	fi, err := os.Stat(path)
	if err != nil || c.now().Sub(fi.ModTime()) > jobDoingFresh {
		return nil
	}
	lines := readTail(path, fi.Size(), 16<<10)
	for i := len(lines) - 1; i >= 0; i-- {
		var ln struct {
			At     string `json:"at"`
			State  string `json:"state"`
			Detail string `json:"detail"`
		}
		if json.Unmarshal(lines[i], &ln) != nil || strings.TrimSpace(ln.Detail) == "" {
			continue
		}
		return &JobDoing{State: ln.State, Detail: truncateRunes(Redact(strings.TrimSpace(ln.Detail)), 200), At: parseTS(ln.At)}
	}
	return nil
}
