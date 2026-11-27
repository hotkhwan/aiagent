// internal/world/procinfo.go
//
// One read of the process table, from /proc on Linux or from ps where there
// is no /proc (macOS). Everything the collector proves about a process —
// that the pid Claude Code recorded is still the same process, which tmux
// pane it runs under, which `claude attach` client a pane holds, what an
// agent pane is really running — comes from here, so the two hosts answer the
// same questions the same way.
package world

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// procInfo is one process, as much of it as the collector needs.
type procInfo struct {
	ppid int
	// start identifies this run of the pid, in the form Claude Code records as
	// procStart on the same host: clock ticks since boot on Linux, the UTC
	// start time in time.ANSIC form on macOS.
	start string
	argv  []string
}

// procTable maps pid → process for one moment.
type procTable map[int]procInfo

func (t procTable) startTime(pid int) (string, bool) {
	p, ok := t[pid]
	if !ok || p.start == "" {
		return "", false
	}
	return p.start, true
}

// emptyBackground is a background process Claude Code started (a warm spare, or
// an agent view nobody has typed into) that holds no conversation yet: nothing
// to show or talk to. It is listed once it has a transcript or a name. A
// terminal left on the agent view (parked) is the same: a window, not an agent.
func emptyBackground(s Session) bool {
	if s.parked && s.sourcePath == "" {
		return true
	}
	return s.Kind == "bg" && s.sourcePath == "" && len(s.SessionID) >= 8 && s.DisplayName == s.SessionID[:8]
}

// keepSession adds s to list unless a session with its id is there already
// (at maps runtime+id to its index). Of two with one id, the one that is not
// a parked terminal wins: a terminal whose conversation moved to the
// background keeps that conversation's sessionId in its registry file, next
// to the job's own file, and dropParked removes it afterwards.
func keepSession(list []Session, at map[string]int, s Session) []Session {
	k := s.RuntimeType + s.SessionID
	if i, ok := at[k]; ok {
		if list[i].parkedJob != "" && s.parkedJob == "" {
			list[i] = s
		}
		return list
	}
	at[k] = len(list)
	return append(list, s)
}

// dropParked removes a terminal whose conversation moved to a background job
// that is itself listed: the same agent would show twice, the stale half
// first. The terminal is kept when its job is not there to stand for it.
func dropParked(sessions []Session) []Session {
	out := sessions[:0]
	for _, s := range sessions {
		dup := false
		if len(s.parkedJob) >= 8 {
			for _, o := range sessions {
				dup = dup || (o.Live && o.SessionID != s.SessionID && strings.HasPrefix(o.SessionID, s.parkedJob))
			}
		}
		if !dup {
			out = append(out, s)
		}
	}
	return out
}

func (t procTable) parent(pid int) int { return t[pid].ppid }

// descendsFrom reports whether pid is ancestor itself or one of its
// descendants (walks at most 16 parents).
func (t procTable) descendsFrom(pid, ancestor int) bool {
	for i := 0; i < 16 && pid > 1; i++ {
		if pid == ancestor {
			return true
		}
		pid = t.parent(pid)
	}
	return false
}

// children maps ppid → child pids.
func (t procTable) children() map[int][]int {
	out := map[int][]int{}
	for pid, p := range t {
		if p.ppid > 0 {
			out[p.ppid] = append(out[p.ppid], pid)
		}
	}
	return out
}

// attachTarget looks below a tmux pane process (up to 3 levels) for a
// `claude attach <id|name>` client and returns its target argument. Text typed
// into that pane is delivered to the attached background session, so the
// session it names — not the attach client process — is the dispatch target.
func (t procTable) attachTarget(panePid int) string {
	kids := t.children()
	queue := []int{panePid}
	for depth := 0; depth < 4 && len(queue) > 0; depth++ {
		var next []int
		for _, pid := range queue {
			argv := t[pid].argv
			if len(argv) >= 3 && filepath.Base(argv[0]) == "claude" && argv[1] == "attach" && argv[2] != "" {
				return argv[2]
			}
			next = append(next, kids[pid]...)
		}
		queue = next
	}
	return ""
}

// readProcTable reads /proc when it exists and ps otherwise. A host with
// neither yields an empty table: nothing is proven alive, which fails closed.
func (c *Collector) readProcTable(ctx context.Context) procTable {
	if fi, err := os.Stat(c.ProcRoot); err == nil && fi.IsDir() {
		return linuxProcTable(c.ProcRoot)
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	out, err := c.Runner.Run(ctx, "ps", "-axww", "-o", "pid=,ppid=,lstart=,args=")
	if err != nil {
		return procTable{}
	}
	return parsePS(out, c.loc())
}

func (c *Collector) loc() *time.Location {
	if c.Loc != nil {
		return c.Loc
	}
	return time.Local
}

// linuxProcTable reads pid, ppid, starttime (field 22) and argv for every
// process under procRoot.
func linuxProcTable(procRoot string) procTable {
	t := procTable{}
	ents, err := os.ReadDir(procRoot)
	if err != nil {
		return t
	}
	for _, e := range ents {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		b, err := os.ReadFile(filepath.Join(procRoot, e.Name(), "stat"))
		if err != nil {
			continue
		}
		s := string(b)
		i := strings.LastIndexByte(s, ')')
		if i < 0 {
			continue
		}
		// fields[0] is field 3 (state): ppid (field 4) is fields[1] and
		// starttime (field 22) is fields[19].
		f := strings.Fields(s[i+1:])
		var p procInfo
		if len(f) > 1 {
			p.ppid, _ = strconv.Atoi(f[1])
		}
		if len(f) > 19 {
			p.start = f[19]
		}
		if cmd, err := os.ReadFile(filepath.Join(procRoot, e.Name(), "cmdline")); err == nil && len(cmd) > 0 {
			p.argv = strings.Split(strings.TrimRight(string(cmd), "\x00"), "\x00")
		}
		t[pid] = p
	}
	return t
}

// psStartLayout is how `ps -o lstart=` prints a start time, read with single
// spaces between fields.
const psStartLayout = "Mon Jan 2 15:04:05 2006"

// parsePS reads `ps -axww -o pid=,ppid=,lstart=,args=`. lstart is always five
// fields in loc; it is converted to UTC in time.ANSIC form, which is what
// Claude Code records as procStart on macOS. args are split on spaces, which
// is enough to read an argv[0] and its first arguments.
func parsePS(out []byte, loc *time.Location) procTable {
	t := procTable{}
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) < 8 {
			continue
		}
		pid, err1 := strconv.Atoi(f[0])
		ppid, err2 := strconv.Atoi(f[1])
		if err1 != nil || err2 != nil {
			continue
		}
		p := procInfo{ppid: ppid, argv: f[7:]}
		if at, err := time.ParseInLocation(psStartLayout, strings.Join(f[2:7], " "), loc); err == nil {
			p.start = at.UTC().Format(time.ANSIC)
		}
		t[pid] = p
	}
	return t
}
