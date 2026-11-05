// internal/aiagent/codex.go
//
// Codex CLI sources (read-only):
//
//	<home>/.codex/sessions/YYYY/MM/DD/rollout-*.jsonl  session_meta, tool calls,
//	                                                  task_started/complete,
//	                                                  token_count (+ rate_limits)
//	<home>/.codex/session_index.jsonl                  thread names
//
// ~/.codex/auth.json is a secret and is never opened.
package world

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type codexLine struct {
	Timestamp string          `json:"timestamp"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
}

type codexMeta struct {
	ID        string `json:"id"`
	Cwd       string `json:"cwd"`
	Timestamp string `json:"timestamp"`
	Git       *struct {
		Branch string `json:"branch"`
	} `json:"git"`
}

type codexPayload struct {
	Type      string          `json:"type"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	Arguments string          `json:"arguments"`
	Info      *struct {
		Total *struct {
			Input  int64 `json:"input_tokens"`
			Cached int64 `json:"cached_input_tokens"`
			Output int64 `json:"output_tokens"`
		} `json:"total_token_usage"`
	} `json:"info"`
	RateLimits *codexRateLimits `json:"rate_limits"`
	Model      string           `json:"model"`
}

type codexWindow struct {
	UsedPercent   float64 `json:"used_percent"`
	WindowMinutes int     `json:"window_minutes"`
	ResetsAt      int64   `json:"resets_at"`
}

type codexRateLimits struct {
	Primary   *codexWindow `json:"primary"`
	Secondary *codexWindow `json:"secondary"`
	PlanType  string       `json:"plan_type"`
}

// codexReading is the freshest provider-reported allowance seen for a home.
type codexReading struct {
	at     time.Time
	limits codexRateLimits
}

func readFirstLine(path string) []byte {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	r := bufio.NewReaderSize(f, 64<<10)
	var buf bytes.Buffer
	for buf.Len() < 8<<20 {
		chunk, isPrefix, err := r.ReadLine()
		buf.Write(chunk)
		if err != nil || !isPrefix {
			break
		}
	}
	return buf.Bytes()
}

func codexThreadNames(home string) map[string]string {
	out := map[string]string{}
	p := filepath.Join(home, ".codex", "session_index.jsonl")
	fi, err := os.Stat(p)
	if err != nil {
		return out
	}
	for _, raw := range readTail(p, fi.Size(), 512<<10) {
		var e struct {
			ID         string `json:"id"`
			ThreadName string `json:"thread_name"`
		}
		if json.Unmarshal(raw, &e) == nil && e.ID != "" && e.ThreadName != "" {
			out[e.ID] = e.ThreadName
		}
	}
	return out
}

// codexProc is a running codex process and its working directory.
type codexProc struct {
	pid    int
	cwd    string
	vscode bool
}

func codexProcesses(procRoot string) []codexProc {
	var out []codexProc
	ents, err := os.ReadDir(procRoot)
	if err != nil {
		return out
	}
	for _, e := range ents {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		cmd, err := os.ReadFile(filepath.Join(procRoot, e.Name(), "cmdline"))
		if err != nil || len(cmd) == 0 {
			continue
		}
		argv0 := string(bytes.SplitN(cmd, []byte{0}, 2)[0])
		if filepath.Base(argv0) != "codex" {
			continue
		}
		cwd, _ := os.Readlink(filepath.Join(procRoot, e.Name(), "cwd"))
		vs := strings.Contains(argv0, "/.vscode") || strings.Contains(argv0, "extensions/openai.")
		if !vs {
			if env, err := os.ReadFile(filepath.Join(procRoot, e.Name(), "environ")); err == nil {
				vs = bytes.Contains(env, []byte("CODEX_INTERNAL_ORIGINATOR_OVERRIDE=codex_vscode"))
			}
		}
		out = append(out, codexProc{pid: pid, cwd: cwd, vscode: vs})
	}
	return out
}

// collectCodex lists rollouts touched since `since` or held open by a live
// process. Liveness evidence, strongest first:
//
//	openFileHandle   — a process holds the rollout file open
//	processCwd       — a codex process runs in the same cwd and this rollout is
//	                   that cwd's most recently written one
func (c *Collector) collectCodex(home, owner string, since time.Time, open map[string]int, procs []codexProc) ([]Session, *codexReading) {
	root := filepath.Join(home, ".codex", "sessions")
	type cand struct {
		path string
		fi   fs.FileInfo
	}
	var cands []cand
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() || !strings.HasSuffix(p, ".jsonl") {
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			return nil
		}
		if _, held := open[p]; held || !fi.ModTime().Before(since) {
			cands = append(cands, cand{p, fi})
		}
		return nil
	})
	if len(cands) == 0 {
		return nil, nil
	}
	names := codexThreadNames(home)
	newestByCwd := map[string]time.Time{}
	var out []Session
	var reading *codexReading
	for _, cd := range cands {
		meta := codexMeta{}
		var first codexLine
		if json.Unmarshal(readFirstLine(cd.path), &first) == nil && first.Type == "session_meta" {
			_ = json.Unmarshal(first.Payload, &meta)
		}
		s := Session{
			SessionID:    meta.ID,
			RuntimeType:  "codexCli",
			ProviderID:   "openai",
			Owner:        owner,
			Cwd:          meta.Cwd,
			RepoID:       repoID(meta.Cwd),
			Status:       StatusEnded,
			LiveEvidence: "none",
			ActivityType: ActivityIdle,
			Usage:        Usage{Source: "codexTokenCount", Complete: true},
			sourcePath:   cd.path,
		}
		if s.SessionID == "" {
			s.SessionID = strings.TrimSuffix(filepath.Base(cd.path), ".jsonl")
		}
		if meta.Git != nil {
			s.GitBranch = meta.Git.Branch
		}
		if t := parseTS(meta.Timestamp); !t.IsZero() {
			s.StartedAt = &t
		}
		s.Title = names[meta.ID]
		s.DisplayName = "Codex · " + s.RepoID

		running := false
		var timeline []TimelineItem
		for _, raw := range readTail(cd.path, cd.fi.Size(), tailBytes) {
			var ln codexLine
			if len(raw) == 0 || json.Unmarshal(raw, &ln) != nil {
				continue
			}
			at := parseTS(ln.Timestamp)
			if !at.IsZero() {
				s.LastEventAt = &at
			}
			var pl codexPayload
			if json.Unmarshal(ln.Payload, &pl) != nil {
				continue
			}
			if ln.Type == "turn_context" && pl.Model != "" {
				s.Model = pl.Model
			}
			switch pl.Type {
			case "task_started":
				running = true
			case "task_complete":
				running = false
			case "custom_tool_call", "function_call":
				cmd := codexCommand(pl)
				act := classify(pl.Name, cmd)
				s.LastTool, s.ActivityType = pl.Name, act
				timeline = append(timeline, TimelineItem{At: at, Tool: pl.Name, ActivityType: act})
			case "token_count":
				if pl.Info != nil && pl.Info.Total != nil {
					s.Usage.InputTokens = pl.Info.Total.Input
					s.Usage.CachedTokens = pl.Info.Total.Cached
					s.Usage.OutputTokens = pl.Info.Total.Output
				}
				if pl.RateLimits != nil && !at.IsZero() && (reading == nil || at.After(reading.at)) {
					reading = &codexReading{at: at, limits: *pl.RateLimits}
				}
			}
		}
		if len(timeline) > timelineMax {
			timeline = timeline[len(timeline)-timelineMax:]
		}
		s.timeline = timeline
		if pid, held := open[cd.path]; held {
			s.Live, s.Pid, s.LiveEvidence = true, pid, "openFileHandle"
		}
		if s.Live {
			if running {
				s.Status = StatusRunning
				if s.ActivityType == ActivityIdle {
					s.ActivityType = ActivityPlanning
				}
			} else {
				s.Status, s.ActivityType = StatusIdle, ActivityIdle
			}
		}
		if s.LastEventAt != nil && s.LastEventAt.After(newestByCwd[s.Cwd]) {
			newestByCwd[s.Cwd] = *s.LastEventAt
		}
		s.Usage.Complete = true
		out = append(out, s)
	}
	// weaker evidence: a codex process in the same cwd owns the newest rollout
	for i := range out {
		s := &out[i]
		if s.Live || s.LastEventAt == nil || !s.LastEventAt.Equal(newestByCwd[s.Cwd]) {
			continue
		}
		for _, p := range procs {
			if p.cwd != "" && p.cwd == s.Cwd {
				s.Live, s.Pid, s.LiveEvidence = true, p.pid, "processCwd"
				s.Status, s.ActivityType = StatusIdle, ActivityIdle
				if c.Now().Sub(*s.LastEventAt) < 2*time.Minute {
					s.Status = StatusRunning
					if s.LastTool == "" {
						s.ActivityType = ActivityPlanning
					} else {
						s.ActivityType = classify(s.LastTool, "")
					}
				}
				break
			}
		}
	}
	return out, reading
}

func codexCommand(pl codexPayload) string {
	if pl.Arguments != "" {
		var a struct {
			Command any `json:"command"`
			Cmd     any `json:"cmd"`
		}
		if json.Unmarshal([]byte(pl.Arguments), &a) == nil {
			return flattenCmd(a.Command) + " " + flattenCmd(a.Cmd)
		}
		return pl.Arguments
	}
	var s string
	if json.Unmarshal(pl.Input, &s) == nil {
		return s
	}
	return string(pl.Input)
}

func flattenCmd(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case []any:
		parts := make([]string, 0, len(x))
		for _, p := range x {
			if s, ok := p.(string); ok {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, " ")
	}
	return ""
}

// codexQuotas turns the freshest provider reading into quota rows. A window
// whose reset time has passed since the reading is reported stale with no
// percentage — the old number no longer describes the allowance.
func codexQuotas(r *codexReading, now time.Time) []Quota {
	if r == nil {
		return []Quota{{ProviderID: "openai", Mode: "guarded", Confidence: "unknown", Source: "no Codex rate_limits reading on this host"}}
	}
	var out []Quota
	add := func(w *codexWindow, label string) {
		if w == nil {
			return
		}
		at := r.at
		q := Quota{
			ProviderID:    "openai",
			WindowMinutes: w.WindowMinutes,
			ObservedAt:    &at,
			PlanType:      r.limits.PlanType,
			Source:        "codex rate_limits " + label,
		}
		if w.ResetsAt > 0 {
			t := time.Unix(w.ResetsAt, 0).UTC()
			q.ResetsAt = &t
		}
		if q.ResetsAt != nil && now.After(*q.ResetsAt) {
			q.Confidence, q.Mode = "stale", "guarded"
		} else {
			used := w.UsedPercent
			rem := 100 - used
			if rem < 0 {
				rem = 0
			}
			q.UsedPercent, q.RemainingPercent = &used, &rem
			q.Confidence, q.Mode = "actual", quotaMode(rem)
		}
		out = append(out, q)
	}
	add(r.limits.Primary, "primary")
	add(r.limits.Secondary, "secondary")
	return out
}

// quotaMode applies the proposal's admission bands (display only in this
// version; this service does not schedule).
func quotaMode(remaining float64) string {
	switch {
	case remaining > 60:
		return "normal"
	case remaining >= 30:
		return "eco"
	case remaining >= 10:
		return "critical"
	}
	return "reserve"
}

// codexPresence adds a read-only "present, no active thread" entry for every
// running codex process that no rollout accounts for (e.g. the VS Code
// extension's app-server before a conversation starts).
func codexPresence(procs []codexProc, sessions []Session) []Session {
	used := map[int]bool{}
	for _, s := range sessions {
		if s.Live && s.Pid > 0 {
			used[s.Pid] = true
		}
	}
	for _, p := range procs {
		if used[p.pid] {
			continue
		}
		name := "Codex"
		if p.vscode {
			name = "Codex · VS Code"
		}
		sessions = append(sessions, Session{
			SessionID:    "codex-proc-" + strconv.Itoa(p.pid),
			RuntimeType:  "codexCli",
			ProviderID:   "openai",
			DisplayName:  name,
			Cwd:          p.cwd,
			RepoID:       repoID(p.cwd),
			Status:       StatusIdle,
			Live:         true,
			LiveEvidence: "process",
			Pid:          p.pid,
			ActivityType: ActivityIdle,
			Kind:         "interactive",
			Title:        "ยังไม่มีบทสนทนาที่กำลังทำงาน",
			Usage:        Usage{Source: "notCounted"},
		})
	}
	return sessions
}
