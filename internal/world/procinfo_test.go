package world

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

// psFixture is `ps -axww -o pid=,ppid=,lstart=,args=` as macOS prints it, in
// local time (Asia/Bangkok here).
const psFixture = `    1     0 Fri Nov  6 08:00:00 2026     /sbin/launchd
23569  1990 Sat Nov 14 01:51:03 2026     /Users/dev/.local/bin/claude
41548     1 Fri Nov  6 08:56:23 2026     /Users/dev/.local/bin/claude --bg
 1990  1980 Sat Nov 14 01:50:00 2026     -zsh
  901  1990 Sat Nov 14 02:00:00 2026     claude attach 580c97ee
`

func bangkok(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Asia/Bangkok")
	if err != nil {
		t.Skip("no tz database:", err)
	}
	return loc
}

// On macOS Claude Code records procStart as the process start time in UTC, in
// time.ANSIC form ("Fri Nov 13 18:51:03 2026"). ps prints the same instant in
// local time, so the table converts it — including the padded single-digit
// day — or no live session would ever match its registry entry.
func TestPsTableGivesStartTimesInClaudesProcStartFormat(t *testing.T) {
	pt := parsePS([]byte(psFixture), bangkok(t))

	cases := map[int]string{
		23569: "Fri Nov 13 18:51:03 2026",
		41548: "Fri Nov  6 01:56:23 2026",
	}
	for pid, want := range cases {
		got, ok := pt.startTime(pid)
		if !ok || got != want {
			t.Errorf("startTime(%d) = %q, %v; want %q", pid, got, ok, want)
		}
	}
	if _, ok := pt.startTime(99999); ok {
		t.Error("a pid that is not running must not report a start time")
	}
	if pp := pt.parent(23569); pp != 1990 {
		t.Errorf("parent(23569) = %d, want 1990", pp)
	}
	if !pt.descendsFrom(23569, 1990) || pt.descendsFrom(41548, 1990) {
		t.Error("descendsFrom should follow the ppid chain")
	}
	if got := pt.attachTarget(1990); got != "580c97ee" {
		t.Errorf("attachTarget below the pane shell = %q, want 580c97ee", got)
	}
}

// Without /proc the collector used to drop every interactive Claude session as
// stale, because it could not prove the recorded process was alive. With the
// ps table it can — and a recycled pid, whose start time differs, still
// counts as gone.
func TestWithoutProcSessionsAreLiveByThePsTable(t *testing.T) {
	e := newEnv(t)
	e.col.ProcRoot = filepath.Join(e.dir, "no-proc-here")
	e.col.Loc = bangkok(t)
	e.runner.psOut = psFixture
	writeFile(t, filepath.Join(e.home, ".claude", "sessions", "23569.json"),
		`{"pid":23569,"sessionId":"live0000-0000-0000-0000-000000000000","cwd":"/root","procStart":"Fri Nov 13 18:51:03 2026","kind":"interactive","status":"idle"}`)
	writeFile(t, filepath.Join(e.home, ".claude", "sessions", "41548.json"),
		`{"pid":41548,"sessionId":"gone0000-0000-0000-0000-000000000000","cwd":"/root","procStart":"Thu Nov  5 01:00:00 2026","kind":"interactive","status":"idle"}`)

	live := map[string]bool{}
	for _, s := range e.col.Snapshot(context.Background()).Sessions {
		live[s.SessionID] = s.Live
	}
	if !live["live0000-0000-0000-0000-000000000000"] {
		t.Error("a running session whose start time matches must be live")
	}
	if live["gone0000-0000-0000-0000-000000000000"] {
		t.Error("a recycled pid (different start time) must not make a session live")
	}
}
