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
// A pinned or workspace id is followed when its conversation moves to a new
// session id (Aliases): a background job by its job id, an interactive
// session (terminal, cmux, VS Code) by its process (Procs: same pid and
// procStart after /clear). Only the main service's own registry feeds either,
// so a room can never claim an id by naming it in its own registry.
//
// Protected-workspace sessions stay hidden from users whatever the admin's
// own duty is: that is the Workspace list, which the guard does not read.
package world

import (
	"context"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
)

// adminListTick is how often the sessions of admin rooms are looked up.
const adminListTick = 10 * time.Second

// ownRoom is the room key of the admin's own token: the account the main
// service runs as (its home's name, "root" on a host install).
func (s *Service) ownRoom() string {
	return filepath.Base(s.Collector.serviceHome())
}

// ownDuty is the admin's own token's role: always admin. It is shown, never
// changed (owner, 2026-11-28): an admin who demoted themself would leave the
// machine with no admin to make one.
func (s *Service) ownDuty(map[string]string) string { return DutyAdmin }

// explicitList brings list up to date under the explicit rules and reports
// whether it changed. seen are main sessions now in a protected workspace;
// rooms, when not nil, are the sessions now seen per admin room (nil for a
// room whose instance did not answer: what was seen before is kept).
// reg is what the main service's own registry shows live now.
func (s *Service) explicitList(list *protectedList, seen []string, rooms map[string][]string, reg liveRegistry) bool {
	before := listKey(*list)
	if !list.Explicit {
		list.Pinned = union(list.Pinned, list.Sessions)
		list.Explicit = true
	}
	list.Workspace = union(list.Workspace, seen)
	listed := union(union(nil, list.Pinned), list.Workspace)
	list.Procs = procsFor(list.Procs, listed, reg)
	list.Aliases = aliasesFor(list.Aliases, listed, list.Procs, reg)
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
	var reg liveRegistry
	if s.Collector != nil {
		snap := s.Collector.Snapshot(ctx).Sessions
		reg = s.ownRegistry(snap)
		seen = s.workspaceSeen(snap)
	}
	protectedMu.Lock()
	defer protectedMu.Unlock()
	list, err := s.readProtected()
	if err != nil {
		return // a corrupt list stays as it is: users are kept out (fail closed)
	}
	if s.explicitList(&list, seen, rooms, reg) {
		s.writeProtected(list)
	}
}

// workspaceSeen are the sessions now running in a protected workspace.
func (s *Service) workspaceSeen(sessions []Session) []string {
	var seen []string
	for _, ss := range sessions {
		if w, ok := s.Collector.lookupWorkspace(ss.DispatchVia); ok && w.protected {
			seen = append(seen, ss.SessionID)
		}
	}
	return seen
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

// liveRegistry is what the main service's own Claude Code registry shows
// live now. Rooms' registries never feed it: a room could write an entry
// naming an admin's id and then move it on to a session of its own.
type liveRegistry struct {
	// jobs maps a background job's short id to the session id it runs under.
	jobs map[string]string
	// procOf maps a live session id (lower case) to its process key.
	procOf map[string]string
	// sessionOn maps a process key to the session id it runs now.
	sessionOn map[string]string
}

// procKey names one process: a pid alone is reused, pid and start time are not.
func procKey(pid int, start string) string { return strconv.Itoa(pid) + ":" + start }

// ownRegistry reads the live registry entries of the main service's own home.
func (s *Service) ownRegistry(sessions []Session) liveRegistry {
	r := liveRegistry{jobs: map[string]string{}, procOf: map[string]string{}, sessionOn: map[string]string{}}
	if s.Collector == nil {
		return r
	}
	own := filepath.Clean(s.Collector.serviceHome())
	for _, ss := range sessions {
		if ss.SessionID == "" || ss.home == "" || filepath.Clean(ss.home) != own {
			continue
		}
		if ss.jobID != "" {
			r.jobs[strings.ToLower(ss.jobID)] = ss.SessionID
		}
		// a parked terminal only shows other sessions: not a conversation
		if ss.RuntimeType == "claudeCode" && ss.Live && ss.Pid > 0 && ss.procStart != "" && !ss.parked {
			k := procKey(ss.Pid, ss.procStart)
			r.procOf[strings.ToLower(ss.SessionID)] = k
			r.sessionOn[k] = ss.SessionID
		}
	}
	return r
}

// procsFor keeps, for each listed id, the process it was last seen live in.
func procsFor(old map[string]string, listed []string, reg liveRegistry) map[string]string {
	out := map[string]string{}
	for _, id := range listed {
		if k := reg.procOf[strings.ToLower(id)]; k != "" {
			out[id] = k
		} else if k := old[id]; k != "" {
			out[id] = k
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// aliasesFor keeps, for each listed id, the session its conversation now runs
// under: its job's (a job's short id is the first part of the session id it
// started with), else its process's (the same Claude Code process after
// /clear). An alias is kept while its id is listed, also when the job or the
// process is gone.
func aliasesFor(old map[string]string, listed []string, procs map[string]string, reg liveRegistry) map[string]string {
	out := map[string]string{}
	for _, id := range listed {
		short := strings.ToLower(id)
		if i := strings.IndexByte(short, '-'); i > 0 {
			short = short[:i]
		}
		byJob := reg.jobs[short]
		byProc := ""
		if k := procs[id]; k != "" {
			byProc = reg.sessionOn[k]
		}
		switch {
		case byJob != "" && !strings.EqualFold(byJob, id):
			out[id] = byJob
		case byProc != "" && !strings.EqualFold(byProc, id):
			out[id] = byProc
		case old[id] != "":
			out[id] = old[id]
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
	for _, k := range sortedKeys(toLists(l.Procs)) {
		b.WriteString("|p:" + k + "=" + l.Procs[k])
	}
	return b.String()
}

// sessionAdminPathRE is the per-session admin toggle. It is answered by the
// main service only (localPath) and is not in userRoutes.
var sessionAdminPathRE = regexp.MustCompile(`^/sessions/[A-Za-z0-9-]+/admin$`)

// handleSessionAdmin puts one of the main service's own sessions on the
// guard's admin list (Pinned) or takes it off. A room's session is an admin
// only through its room's duty, so it is refused here.
func (s *Service) handleSessionAdmin(c fiber.Ctx) error {
	id := strings.Clone(c.Params("id"))
	var body struct {
		On *bool `json:"on"`
	}
	if err := c.Bind().Body(&body); err != nil || body.On == nil {
		return s.fail(c, ErrBadRequest(`expected {"on": true|false}`))
	}
	if id == "" || !sessionIDRE.MatchString(id) {
		return s.fail(c, ErrBadRequest("session id ไม่ถูกต้อง"))
	}
	on := *body.On
	return s.mutate(c, "aiagent.sessionAdmin", []string{id, strconv.FormatBool(on)}, func() (any, error) {
		if s.Duties == nil {
			return nil, ErrConflict("ตั้ง admin ราย session ได้เมื่อแต่ละ token มีห้องของตัวเอง (--os-users)")
		}
		ctx := context.Background()
		snap := s.Collector.Snapshot(ctx).Sessions
		reg := s.ownRegistry(snap)
		if on {
			if err := s.ownSession(ctx, snap, id); err != nil {
				return nil, err
			}
		}
		seen := s.workspaceSeen(snap)
		protectedMu.Lock()
		defer protectedMu.Unlock()
		list, err := s.readProtected()
		if err != nil {
			return nil, err
		}
		if on {
			list.Pinned = union(list.Pinned, []string{id})
		} else {
			// the card may show the id the conversation runs under now
			kept := list.Pinned[:0:0]
			for _, p := range list.Pinned {
				if strings.EqualFold(p, id) || strings.EqualFold(list.Aliases[p], id) {
					delete(list.Aliases, p)
					delete(list.Procs, p)
					continue
				}
				kept = append(kept, p)
			}
			list.Pinned = kept
		}
		s.explicitList(&list, seen, nil, reg)
		s.writeProtected(list)
		admin := idSet(list.Sessions)[strings.ToLower(id)]
		return fiber.Map{"sessionId": id, "on": on, "admin": admin}, nil
	})
}

// ownSession checks that id is a session of the main service's own home.
func (s *Service) ownSession(ctx context.Context, snap []Session, id string) error {
	own := filepath.Clean(s.Collector.serviceHome())
	for _, ss := range snap {
		if strings.EqualFold(ss.SessionID, id) {
			if ss.home != "" && filepath.Clean(ss.home) == own {
				return nil
			}
			return ErrConflict("session นี้เป็นของห้องอื่น — ให้เป็น admin ได้ทางบทบาทของห้อง (admin) เท่านั้น")
		}
	}
	if s.OSUsers != nil && s.teammateOf(ctx, "", id) != "" {
		return ErrConflict("session นี้เป็นของห้องอื่น — ให้เป็น admin ได้ทางบทบาทของห้อง (admin) เท่านั้น")
	}
	return ErrNotFound("ไม่พบ session นี้ในเครื่องของ admin")
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
	own, home := "", ""
	if s.Duties != nil {
		duties = s.Duties.Load()
		own = s.ownRoom()
		home = filepath.Clean(s.Collector.serviceHome())
	}
	for i := range sessions {
		ss := &sessions[i]
		// the admin can pin only the main service's own sessions
		ss.Pinnable = home != "" && ss.home != "" && filepath.Clean(ss.home) == home
		switch {
		case admins[strings.ToLower(ss.SessionID)]:
			ss.Role = DutyAdmin
		case ss.Owner != "" && ss.Owner != own && duties[ss.Owner] != "":
			ss.Role = duties[ss.Owner]
		}
	}
}
