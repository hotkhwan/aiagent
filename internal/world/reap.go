// internal/world/reap.go
//
// A relay (relay.go) is only a typing channel. One left behind after its
// attach ended, or not used for a while, is clutter in `tmux list-sessions`
// that looks like an agent, so it is closed. The session it typed into is a
// job of Claude Code's daemon and goes on running.
package world

import (
	"context"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const relayIdle = 10 * time.Minute

var relaySessionRE = regexp.MustCompile(`^aiagent-[0-9a-f]{8}$`)

// staleRelays picks the relays to close from `list-panes -a` rows of
// "session|dead|attached|activity".
func staleRelays(out string, now time.Time) []string {
	var stale []string
	for _, row := range strings.Split(out, "\n") {
		f := strings.Split(strings.TrimSpace(row), "|")
		if len(f) != 4 || !relaySessionRE.MatchString(f[0]) {
			continue
		}
		at, err := strconv.ParseInt(f[3], 10, 64)
		if err != nil || f[2] != "0" {
			continue
		}
		idle := now.Sub(time.Unix(at, 0))
		// a dead pane still holds the reason a fresh attach was refused
		if idle > relayIdle || (f[1] == "1" && idle > time.Minute) {
			stale = append(stale, f[0])
		}
	}
	return stale
}

func (c *Collector) reapRelays(ctx context.Context, now time.Time) {
	out, err := c.tmux(ctx, "list-panes", "-a", "-F", "#{session_name}|#{pane_dead}|#{session_attached}|#{session_activity}")
	if err != nil {
		return
	}
	for _, name := range staleRelays(out, now) {
		unlock := c.lockSend("relay:" + name)
		_, _ = c.tmux(ctx, "kill-session", "-t", "="+name)
		unlock()
	}
}
