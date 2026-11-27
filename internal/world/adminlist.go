// internal/world/adminlist.go
//
// The admin list. protected.json names the sessions the host's guard treats
// as the admin's, and users are kept out of. It used to grow by itself: any
// session seen in a workspace marked protected was written in, so a session
// started there for some other job became an admin to the guard. With room
// duties the list is explicit, built only from:
//
//   - Pinned: ids put there by hand, and the ids that were in the file when
//     it was first kept this way (nobody loses admin on an upgrade);
//   - the main service's own protected-workspace sessions, only while the
//     admin's own room (the service's account, "self" on the web) is admin;
//     unset reads as admin, so the old behaviour holds until changed;
//   - Rooms: every session seen in a room whose duty is admin.
//
// Protected-workspace sessions stay hidden from users whatever the admin's
// own duty is: that is the Workspace list, which the guard does not read.
package world

import (
	"context"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// adminListTick is how often the sessions of admin rooms are looked up.
const adminListTick = 10 * time.Second

// ownRoom is the room key of the admin's own token: the account the main
// service runs as (its home's name, "root" on a host install).
func (s *Service) ownRoom() string {
	return filepath.Base(s.Collector.serviceHome())
}

// ownDuty is the admin's own room's duty; unset is admin.
func (s *Service) ownDuty(duties map[string]string) string {
	if d := duties[s.ownRoom()]; d != "" {
		return d
	}
	return DutyAdmin
}

// explicitList brings list up to date under the explicit rules and reports
// whether it changed. seen are main sessions now in a protected workspace;
// rooms, when not nil, are the sessions now seen per admin room (nil for a
// room whose instance did not answer: what was seen before is kept).
// jobs maps a background job's short id to the session id it runs under now.
func (s *Service) explicitList(list *protectedList, seen []string, rooms map[string][]string, jobs map[string]string) bool {
	before := listKey(*list)
	if !list.Explicit {
		list.Pinned = union(list.Pinned, list.Sessions)
		list.Explicit = true
	}
	list.Workspace = union(list.Workspace, seen)
	list.Aliases = aliasesFor(list.Aliases, union(union(nil, list.Pinned), list.Workspace), jobs)
	duties := s.Duties.Load()
	next := map[string][]string{}
	for room, ids := range list.Rooms {
		if duties[room] == DutyAdmin {
			next[room] = ids
		}
	}
	for room, ids := range rooms {
		if duties[room] == DutyAdmin {
			next[room] = union(next[room], ids)
		}
	}
	if len(next) == 0 {
		next = nil
	}
	list.Rooms = next
	want := withAliases(union(nil, list.Pinned), list.Aliases)
	if s.ownDuty(duties) == DutyAdmin {
		want = union(want, withAliases(list.Workspace, list.Aliases))
	}
	for _, room := range sortedKeys(list.Rooms) {
		want = union(want, list.Rooms[room])
	}
	if want == nil {
		want = []string{}
	}
	list.Sessions = want
	return listKey(*list) != before
}

// syncAdminList looks up the sessions of every room set to admin and
// writes the list when it changed.
func (s *Service) syncAdminList(ctx context.Context) {
	if s.Duties == nil || s.Store == nil {
		return
	}
	rooms := map[string][]string{}
	if s.OSUsers != nil {
		duties := s.Duties.Load()
		for _, name := range s.roomNames() {
			if duties[name] != DutyAdmin {
				continue
			}
			var ids []string
			for _, ss := range s.instanceSessions(ctx, name) {
				if ss.SessionID != "" {
					ids = append(ids, ss.SessionID)
				}
			}
			rooms[name] = ids
		}
	}
	var seen []string
	var jobs map[string]string
	if s.Collector != nil {
		snap := s.Collector.Snapshot(ctx).Sessions
		jobs = jobSessions(snap)
		for _, ss := range snap {
			if w, ok := s.Collector.lookupWorkspace(ss.DispatchVia); ok && w.protected {
				seen = append(seen, ss.SessionID)
			}
		}
	}
	protectedMu.Lock()
	defer protectedMu.Unlock()
	list, err := s.readProtected()
	if err != nil {
		return // a corrupt list stays as it is: users are kept out (fail closed)
	}
	if s.explicitList(&list, seen, rooms, jobs) {
		s.writeProtected(list)
	}
}

// StartAdminList keeps the admin rooms' sessions on the list.
func (s *Service) StartAdminList(ctx context.Context) {
	if s.Duties == nil {
		return
	}
	go func() {
		t := time.NewTicker(adminListTick)
		defer t.Stop()
		for {
			if s.Store.Enabled() {
				s.syncAdminList(ctx)
			}
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()
}

// union appends to a the ids of b it does not have (case-insensitive),
// keeping a's order.
func union(a, b []string) []string {
	have := idSet(a)
	for _, id := range b {
		if id == "" || have[strings.ToLower(id)] {
			continue
		}
		have[strings.ToLower(id)] = true
		a = append(a, id)
	}
	return a
}

// jobSessions maps each live background job's short id to its session id.
func jobSessions(sessions []Session) map[string]string {
	m := map[string]string{}
	for _, ss := range sessions {
		if ss.jobID != "" && ss.SessionID != "" {
			m[strings.ToLower(ss.jobID)] = ss.SessionID
		}
	}
	return m
}

// aliasesFor keeps, for each listed id, the session its job now runs under.
// A job's short id is the first part of the session id it started with. An
// alias is kept while its id is listed, also when the job is not running.
func aliasesFor(old map[string]string, listed []string, jobs map[string]string) map[string]string {
	out := map[string]string{}
	for _, id := range listed {
		short := strings.ToLower(id)
		if i := strings.IndexByte(short, '-'); i > 0 {
			short = short[:i]
		}
		if now := jobs[short]; now != "" && !strings.EqualFold(now, id) {
			out[id] = now
		} else if a := old[id]; a != "" {
			out[id] = a
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// withAliases is ids followed by the sessions their jobs run under now.
func withAliases(ids []string, aliases map[string]string) []string {
	out := union(nil, ids)
	for _, id := range ids {
		if a := aliases[id]; a != "" {
			out = union(out, []string{a})
		}
	}
	return out
}

func sortedKeys(m map[string][]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func listKey(l protectedList) string {
	var b strings.Builder
	b.WriteString(strings.Join(l.Sessions, ",") + "|" + strings.Join(l.Pinned, ",") + "|" + strings.Join(l.Workspace, ","))
	if l.Explicit {
		b.WriteString("|e")
	}
	for _, k := range sortedKeys(l.Rooms) {
		b.WriteString("|" + k + ":" + strings.Join(l.Rooms[k], ","))
	}
	for _, k := range sortedKeys(toLists(l.Aliases)) {
		b.WriteString("|a:" + k + "=" + l.Aliases[k])
	}
	return b.String()
}

func toLists(m map[string]string) map[string][]string {
	out := make(map[string][]string, len(m))
	for k, v := range m {
		out[k] = []string{v}
	}
	return out
}

// markRoles names each session's role for the admin's cards: admin when it is
// on the guard's list, otherwise the duty of the room it runs in. A session
// of the main service that is not on the list gets none.
func (s *Service) markRoles(sessions []Session) {
	var admins map[string]bool
	protectedMu.Lock()
	if list, err := s.readProtected(); err == nil {
		admins = idSet(list.Sessions)
		if s.Duties == nil {
			admins = idSet(union(list.Sessions, list.Pinned))
		}
	}
	protectedMu.Unlock()
	duties := map[string]string{}
	own := ""
	if s.Duties != nil {
		duties = s.Duties.Load()
		own = s.ownRoom()
	}
	for i := range sessions {
		ss := &sessions[i]
		switch {
		case admins[strings.ToLower(ss.SessionID)]:
			ss.Role = DutyAdmin
		case ss.Owner != "" && ss.Owner != own && duties[ss.Owner] != "":
			ss.Role = duties[ss.Owner]
		}
	}
}
