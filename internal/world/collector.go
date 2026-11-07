// internal/world/collector.go
package world

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Collector builds snapshots of AI agent activity on this host and performs
// the agcmd-backed actions. It is safe for concurrent use.
type Collector struct {
	// Homes lists home directories to scan. Empty → /root plus every
	// /home/* that contains .claude or .codex.
	Homes      []string
	ProcRoot   string
	RunsDir    string
	AgcmdPath  string
	TmuxPath   string
	Runner     Runner
	Now        func() time.Time
	CacheTTL   time.Duration
	HistoryMax int
	// Workspaces are the named checkouts an agent may be dispatched into,
	// loaded from the operator's config (workspaces.go). Empty is valid and
	// means view-only: every dispatch is refused as "unknown workspace".
	Workspaces []workspaceEntry
	// ClaudePath runs `claude stop`; ServiceHome is whose bg sessions this
	// service may stop (its own; default $HOME).
	ClaudePath  string
	CodexPath   string
	ServiceHome string
	Sleep       func(time.Duration)

	mu             sync.Mutex
	transcripts    map[string]*transcriptState
	transcriptPath map[string]string
	cached         *Snapshot
	cachedAt       time.Time

	daemonMu    sync.Mutex
	daemonCache []daemonEntry
	daemonAt    time.Time
}

// NewCollector returns a collector with the packaged defaults, overridable
// from the environment (AIAGENT_HOMES colon-separated, AGCMD_BIN,
// AGCMD_RUNS, AIAGENT_TMUX).
func NewCollector() *Collector {
	c := &Collector{
		ProcRoot:   "/proc",
		RunsDir:    envOr("AGCMD_RUNS", filepath.Join(DefaultStateDir(), "runs")),
		AgcmdPath:  envOr("AGCMD_BIN", findBin("agcmd")),
		TmuxPath:   envOr("AIAGENT_TMUX", "tmux"),
		ClaudePath: envOr("AIAGENT_CLAUDE", findClaude()),
		CodexPath:  envOr("AIAGENT_CODEX", findBin("codex")),
		Runner:     ExecRunner{},
		CacheTTL:   3 * time.Second,
		HistoryMax: 60,
	}
	if v := os.Getenv("AIAGENT_HOMES"); v != "" {
		for _, h := range strings.Split(v, ":") {
			if h = strings.TrimSpace(h); h != "" {
				c.Homes = append(c.Homes, h)
			}
		}
	}
	return c
}

func findClaude() string {
	if p, err := exec.LookPath("claude"); err == nil {
		return p
	}
	if h, err := os.UserHomeDir(); err == nil {
		if p := filepath.Join(h, ".local", "bin", "claude"); fileExists(p) {
			return p
		}
	}
	return "claude"
}

func findBin(name string) string {
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	if h, err := os.UserHomeDir(); err == nil {
		if p := filepath.Join(h, ".local", "bin", name); fileExists(p) {
			return p
		}
	}
	return name
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func jsonUnmarshal(b []byte, v any) error { return json.Unmarshal(b, v) }

func (c *Collector) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c *Collector) homes() []string {
	if len(c.Homes) > 0 {
		return c.Homes
	}
	out := []string{"/root"}
	ents, _ := os.ReadDir("/home")
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		h := filepath.Join("/home", e.Name())
		for _, d := range []string{".claude", ".codex"} {
			if fi, err := os.Stat(filepath.Join(h, d)); err == nil && fi.IsDir() {
				out = append(out, h)
				break
			}
		}
	}
	return out
}

// Snapshot returns the current state, served from a short cache so many open
// browser tabs cost one collection.
func (c *Collector) Snapshot(ctx context.Context) Snapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cached != nil && c.now().Sub(c.cachedAt) < c.CacheTTL {
		return *c.cached
	}
	snap := c.collectLocked(ctx)
	snap.nonNil()
	c.cached, c.cachedAt = &snap, c.now()
	return snap
}

// nonNil replaces nil slices with empty ones before the snapshot is sent.
//
// A nil slice marshals to `null`, not `[]`, and the browser then reads
// `st.snap.runs.length` off null. That threw inside render(); poll() caught it
// along with everything else, and the list view drew nothing at all — no
// error, no empty state, a blank screen on any machine that happened to have
// no agcmd runs. Only the list view touched runs.length, so it survived every
// session that stayed on the map.
func (s *Snapshot) nonNil() {
	if s.Sessions == nil {
		s.Sessions = []Session{}
	}
	if s.Runs == nil {
		s.Runs = []Run{}
	}
	if s.Workspaces == nil {
		s.Workspaces = []Workspace{}
	}
	if s.Quotas == nil {
		s.Quotas = []Quota{}
	}
	if s.Warnings == nil {
		s.Warnings = []string{}
	}
}

func startOfDay(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, t.Location())
}

func (c *Collector) collectLocked(ctx context.Context) Snapshot {
	if c.transcripts == nil {
		c.transcripts = map[string]*transcriptState{}
		c.transcriptPath = map[string]string{}
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	now := c.now()
	since := startOfDay(now.In(time.Local))
	host, _ := os.Hostname()
	snap := Snapshot{GeneratedAt: now.UTC(), Host: host, Sessions: []Session{}, Runs: []Run{}, Warnings: []string{}}

	open := openFiles(c.ProcRoot, ".jsonl")
	procs := codexProcesses(c.ProcRoot)
	var reading *codexReading
	seen := map[string]bool{}
	for _, h := range c.homes() {
		owner := filepath.Base(h)
		for _, s := range c.collectClaude(h, owner, since) {
			if !seen[s.RuntimeType+s.SessionID] {
				seen[s.RuntimeType+s.SessionID] = true
				snap.Sessions = append(snap.Sessions, s)
			}
		}
		ss, r := c.collectCodex(h, owner, since, open, procs)
		for _, s := range ss {
			if !seen[s.RuntimeType+s.SessionID] {
				seen[s.RuntimeType+s.SessionID] = true
				snap.Sessions = append(snap.Sessions, s)
			}
		}
		if r != nil && (reading == nil || r.at.After(reading.at)) {
			reading = r
		}
	}
	snap.Sessions = c.mergeDaemon(ctx, snap.Sessions)
	snap.Sessions = codexPresence(procs, snap.Sessions)
	c.forgetUnseen(snap.Sessions)

	tm := c.readTmux(ctx)
	snap.Workspaces = c.workspaces(tm)
	snap.Runs = c.readRuns(tm)
	if _, err := os.Stat(c.AgcmdPath); err != nil {
		snap.Warnings = append(snap.Warnings, "agcmd not found at "+c.AgcmdPath+" — sending work is unavailable")
	}

	// panes running `claude attach <id>` deliver typed text to that session
	attached := map[string]string{} // attach target → workspace
	if len(tm.agentPid) > 0 {
		kids := childMap(c.ProcRoot)
		for _, w := range snap.Workspaces {
			if pane := tm.agentPid[w.TmuxSession]; w.AgentReady && pane > 0 {
				if t := attachTarget(c.ProcRoot, pane, kids); t != "" {
					attached[t] = w.Name
				}
			}
		}
	}
	for i := range snap.Sessions {
		ss := &snap.Sessions[i]
		ss.Workspace = c.workspaceFor(ss.Cwd)
		for t, ws := range attached {
			if ss.Live && ss.RuntimeType == "claudeCode" && (len(t) >= 8 && strings.HasPrefix(ss.SessionID, t) || t == ss.DisplayName) {
				ss.DispatchVia = ws
			}
		}
		if ss.DispatchVia != "" || !ss.Live || ss.Pid <= 0 {
			continue
		}
		for _, w := range snap.Workspaces {
			if pane := tm.agentPid[w.TmuxSession]; w.AgentReady && pane > 0 && descendsFrom(c.ProcRoot, ss.Pid, pane) {
				ss.DispatchVia = w.Name
				ss.inPane = w.Name
				break
			}
		}
	}
	for i := range snap.Sessions {
		c.decideStop(&snap.Sessions[i])
		c.decideMessage(&snap.Sessions[i])
	}
	sort.SliceStable(snap.Sessions, func(i, j int) bool {
		a, b := snap.Sessions[i], snap.Sessions[j]
		if a.Live != b.Live {
			return a.Live
		}
		return lastEvent(a).After(lastEvent(b))
	})
	// cap ended history
	live, hist := 0, 0
	kept := snap.Sessions[:0]
	for _, s := range snap.Sessions {
		if s.Live {
			live++
			kept = append(kept, s)
			continue
		}
		hist++
		if hist <= c.HistoryMax {
			kept = append(kept, s)
		}
	}
	snap.Sessions = kept

	for _, s := range snap.Sessions {
		switch s.Status {
		case StatusRunning:
			snap.Summary.Running++
		case StatusIdle:
			snap.Summary.Idle++
		case StatusNeedsInput:
			snap.Summary.NeedsInput++
		case StatusCompleted:
			snap.Summary.Completed++
		case StatusEnded:
			snap.Summary.EndedToday++
		}
	}
	for _, r := range snap.Runs {
		if r.Status == "WAITING_DECISION" {
			snap.Summary.WaitingApproval++
		}
		if r.Lost {
			snap.Summary.Lost++
		}
		upd := parseTS(r.UpdatedAt)
		if upd.IsZero() || upd.Before(since) {
			continue
		}
		switch r.Status {
		case "DONE", "READY":
			snap.Summary.RunsDoneToday++
		case "ABORTED":
			snap.Summary.RunsAbortedToday++
		}
	}

	snap.Quotas = append(codexQuotas(reading, now), c.claudeQuotas(now)...)
	return snap
}

// forgetUnseen drops cached transcript state for files no longer listed so
// the cache cannot grow without bound.
func (c *Collector) forgetUnseen(ss []Session) {
	keep := map[string]bool{}
	for _, s := range ss {
		keep[s.sourcePath] = true
	}
	for p := range c.transcripts {
		if !keep[p] {
			delete(c.transcripts, p)
		}
	}
	if len(c.transcriptPath) > 1000 {
		c.transcriptPath = map[string]string{}
	}
}

func filepathBase(p string) string { return filepath.Base(p) }

func lastEvent(s Session) time.Time {
	if s.LastEventAt != nil {
		return *s.LastEventAt
	}
	if s.StartedAt != nil {
		return *s.StartedAt
	}
	return time.Time{}
}

// decideStop picks the only supported stop path for a live session, or
// records why the web must not stop it.
func (c *Collector) decideStop(ss *Session) {
	if !ss.Live {
		return
	}
	switch {
	case ss.inPane != "":
		ss.StopVia = "agcmdRestart"
	case ss.RuntimeType == "claudeCode" && ss.Kind == "bg" && ss.home == c.serviceHome():
		ss.StopVia = "claudeStop"
	case ss.RuntimeType == "claudeCode" && ss.Kind == "bg":
		ss.StopBlocked = "background session ของผู้ใช้ " + ss.Owner + " — บริการนี้หยุดได้เฉพาะ session ของ " + filepath.Base(c.serviceHome())
	case ss.RuntimeType == "claudeCode":
		ss.StopBlocked = "เปิดอยู่ในเทอร์มินัล/VS Code ของผู้ใช้ (interactive) — ปิดจากที่นั่น เว็บไม่ปิดให้"
	default:
		ss.StopBlocked = "ไม่ได้รันใน tmux ที่ agcmd ดูแล — ไม่มีวิธีหยุดที่รองรับและปลอดภัย"
	}
}

func (c *Collector) serviceHome() string {
	if c.ServiceHome != "" {
		return c.ServiceHome
	}
	h, err := os.UserHomeDir()
	if err != nil {
		return "/root"
	}
	return h
}

// invalidate drops the cached snapshot so the next read reflects an action.
func (c *Collector) invalidate() {
	c.mu.Lock()
	c.cached = nil
	c.mu.Unlock()
	// after a write (stop, message, login) the agent view may have changed
	c.daemonMu.Lock()
	c.daemonCache = nil
	c.daemonMu.Unlock()
}

// SessionDetail is a session plus its recent tool timeline.
type SessionDetail struct {
	Session  Session        `json:"session"`
	Timeline []TimelineItem `json:"timeline"`
}

// Detail finds one session in the current snapshot.
func (c *Collector) Detail(ctx context.Context, id string) (SessionDetail, error) {
	snap := c.Snapshot(ctx)
	for _, s := range snap.Sessions {
		if s.SessionID == id {
			tl := s.timeline
			if tl == nil {
				tl = []TimelineItem{}
			}
			return SessionDetail{Session: s, Timeline: tl}, nil
		}
	}
	return SessionDetail{}, ErrNotFound("session not found")
}
