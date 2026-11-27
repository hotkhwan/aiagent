package world

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func readList(t *testing.T, dir string) protectedList {
	t.Helper()
	var l protectedList
	b, err := os.ReadFile(filepath.Join(dir, protectedFile))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &l); err != nil {
		t.Fatal(err)
	}
	return l
}

// TestAdminListIsExplicit: with room duties the guard's list holds only what
// the admin chose. The ids already there are kept (pinned); a session in a
// protected workspace is an admin only while the admin's own room is admin,
// and is hidden from users either way; a room set to admin puts its sessions
// on the list and taking the duty away takes them off.
func TestAdminListIsExplicit(t *testing.T) {
	e, _, o := osUserEnv(t)
	e.svc.Duties = &RoomDuties{Path: filepath.Join(t.TempDir(), "room-duties.conf")}
	e.col.Workspaces = []workspaceEntry{{name: "admin", session: "admin", dir: "/x", provider: "claude", protected: true}}
	if _, err := e.store.Enable("t"); err != nil {
		t.Fatal(err)
	}
	_, _, _ = e.store.AddUser("tiew", "tiew")
	_, _, _ = e.store.AddUser("lew", "lew")
	serve := func(sessions string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"code":"SUCCESS","details":{"sessions":` + sessions + `}}`))
		}
	}
	instance(t, o, "tiew", serve(`[{"sessionId":"tiew-sess-1","live":true}]`))
	instance(t, o, "lew", serve(`[{"sessionId":"lew-sess-1","live":true}]`))
	if err := os.WriteFile(filepath.Join(e.store.dir, protectedFile), []byte(`{"sessions":["old-admin-1"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	svc := e.svc

	// upgrade: what was there stays an admin
	p := svc.protectionsFor([]Session{{SessionID: "ws-sess-1", DispatchVia: "admin"}})
	l := readList(t, e.store.dir)
	if !l.Explicit || !slices.Contains(l.Pinned, "old-admin-1") {
		t.Fatalf("upgrade lost the old list: %+v", l)
	}
	// own room unset reads as admin: the workspace session is on the list
	if !slices.Equal(l.Sessions, []string{"old-admin-1", "ws-sess-1"}) || !p.covers("ws-sess-1") {
		t.Fatalf("sessions = %v", l.Sessions)
	}

	// the admin's own room is no longer admin: off the guard's list, still hidden from users
	if err := svc.Duties.Set(svc.ownRoom(), DutyBackend); err != nil {
		t.Fatal(err)
	}
	p = svc.protectionsFor(nil)
	l = readList(t, e.store.dir)
	if slices.Contains(l.Sessions, "ws-sess-1") || !p.covers("ws-sess-1") {
		t.Fatalf("own room not admin: sessions = %v covered = %v", l.Sessions, p.covers("ws-sess-1"))
	}

	// a room set to admin: its sessions join the list
	if err := svc.Duties.Set("tiew", DutyAdmin); err != nil {
		t.Fatal(err)
	}
	svc.syncAdminList(context.Background())
	l = readList(t, e.store.dir)
	if !slices.Contains(l.Sessions, "tiew-sess-1") || slices.Contains(l.Sessions, "lew-sess-1") {
		t.Fatalf("admin room: sessions = %v", l.Sessions)
	}
	p = svc.protectionsFor(nil)
	if p.covers("tiew-sess-1") || p.adminRoom["tiew-sess-1"] != "tiew" {
		t.Fatal("an admin room's session must stay its own room's, not be locked like the admin's")
	}
	// other rooms do not read it; the admin does
	if mates, _ := svc.teammates(context.Background(), "lew"); slices.ContainsFunc(mates, func(s Session) bool { return s.SessionID == "tiew-sess-1" }) {
		t.Fatal("another room reads an admin room's session")
	}
	if mates, _ := svc.teammates(context.Background(), ""); !slices.ContainsFunc(mates, func(s Session) bool { return s.SessionID == "tiew-sess-1" }) {
		t.Fatal("the admin lost an admin room's session")
	}

	// the duty taken away: off the list at once
	if err := svc.Duties.Set("tiew", DutyFrontend); err != nil {
		t.Fatal(err)
	}
	svc.protectionsFor(nil)
	l = readList(t, e.store.dir)
	if slices.Contains(l.Sessions, "tiew-sess-1") || l.Rooms != nil {
		t.Fatalf("duty removed but still listed: %+v", l)
	}
	if !slices.Contains(l.Sessions, "old-admin-1") {
		t.Fatal("a pinned id was dropped")
	}
}

// TestOwnTokenDuty: the token list shows the admin's own room with its duty
// (admin until changed), the admin can change it, and it cannot be unset.
func TestOwnTokenDuty(t *testing.T) {
	e, _, _ := osUserEnv(t)
	e.svc.Duties = &RoomDuties{Path: filepath.Join(t.TempDir(), "room-duties.conf")}
	admin, err := e.store.Enable("t")
	if err != nil {
		t.Fatal(err)
	}
	next := func() { e.svc.lastWrite = e.svc.lastWrite.Add(-writeSpacing) }
	_, m, body := e.do(t, "GET", apiPrefix+"/tokens", admin, "")
	self, _ := m["details"].(map[string]any)["self"].(map[string]any)
	if self == nil || self["duty"] != DutyAdmin || self["room"] != e.svc.ownRoom() {
		t.Fatalf("self = %s", body)
	}
	if code, _, _ := e.do(t, "POST", apiPrefix+"/tokens/self/duty", admin, `{"duty":""}`); code != 400 {
		t.Fatalf("own duty unset = %d", code)
	}
	next()
	if code, _, body := e.do(t, "POST", apiPrefix+"/tokens/self/duty", admin, `{"duty":"backend"}`); code != 200 {
		t.Fatalf("own duty = %d %s", code, body)
	}
	if got := e.svc.Duties.Load()[e.svc.ownRoom()]; got != DutyBackend {
		t.Fatalf("own duty stored = %q", got)
	}
	if _, _, body := e.do(t, "GET", apiPrefix+"/tokens", admin, ""); !strings.Contains(body, `"self":{"duty":"backend"`) {
		t.Fatalf("list = %s", body)
	}
}

// TestAdminListWithoutDuties: an install without rooms keeps the old,
// self-growing list exactly.
func TestAdminListWithoutDuties(t *testing.T) {
	e := newEnv(t)
	e.col.Workspaces = []workspaceEntry{{name: "admin", session: "admin", dir: "/x", provider: "claude", protected: true}}
	if _, err := e.store.Enable("t"); err != nil {
		t.Fatal(err)
	}
	svc := &Service{Store: e.store, Collector: e.col}
	svc.protectionsFor([]Session{{SessionID: "abcd1234", DispatchVia: "admin"}})
	l := readList(t, e.store.dir)
	if l.Explicit || !slices.Equal(l.Sessions, []string{"abcd1234"}) || l.Pinned != nil {
		t.Fatalf("list = %+v", l)
	}
}

// TestAdminListFollowsAJob: a pinned background job whose conversation moved
// to a new session id (registry: same jobId, new sessionId) is an admin under
// the new id too, the one the guard sees, and stays so after the job stops.
// Seen on ata-core: pinned 3ef9360e-…, the job ran on as 13c52b46-….
func TestAdminListFollowsAJob(t *testing.T) {
	e, _, _ := osUserEnv(t)
	e.svc.Duties = &RoomDuties{Path: filepath.Join(t.TempDir(), "room-duties.conf")}
	if _, err := e.store.Enable("t"); err != nil {
		t.Fatal(err)
	}
	const old, now = "3ef9360e-e900-4b56-a4a6-a041ecf2cc97", "13c52b46-8701-43f2-9eeb-01d89932b63d"
	if err := os.WriteFile(filepath.Join(e.store.dir, protectedFile), []byte(`{"sessions":["`+old+`"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	svc := e.svc
	p := svc.protectionsFor([]Session{
		{SessionID: now, jobID: "3ef9360e", home: e.home},
		{SessionID: "other-1", jobID: "aaaa0000", home: e.home},
		// a room's registry (another home) naming the pinned job's short id
		{SessionID: "room-1", jobID: "3ef9360e", home: "/home/tiew"},
	})
	l := readList(t, e.store.dir)
	if !slices.Equal(l.Sessions, []string{old, now}) || l.Aliases[old] != now {
		t.Fatalf("list = %+v", l)
	}
	if !p.covers(now) || p.covers("other-1") || p.covers("room-1") {
		t.Fatal("the job's new session must be hidden from users like the pinned one, and nothing else")
	}
	// the job is not running: the alias stays
	svc.protectionsFor(nil)
	if l = readList(t, e.store.dir); !slices.Contains(l.Sessions, now) {
		t.Fatalf("alias forgotten: %+v", l)
	}
	// a room set to admin never names the admin's jobs: only the main
	// service's own registry feeds jobs
	if got := svc.ownRegistry([]Session{{SessionID: "x", jobID: "", home: e.home}, {SessionID: "y", jobID: "1234abcd", home: "/home/tiew"}}).jobs; len(got) != 0 {
		t.Fatalf("jobs = %v", got)
	}
}

// TestMarkRoles: the admin's cards say admin for a session on the guard's
// list and the room's duty for a room's session; the main service's other
// sessions get no role.
func TestMarkRoles(t *testing.T) {
	e, _, _ := osUserEnv(t)
	e.svc.Duties = &RoomDuties{Path: filepath.Join(t.TempDir(), "room-duties.conf")}
	if _, err := e.store.Enable("t"); err != nil {
		t.Fatal(err)
	}
	_ = e.svc.Duties.Set("tiew", DutyFrontend)
	_ = e.svc.Duties.Set("lew", DutyTester)
	if err := os.WriteFile(filepath.Join(e.store.dir, protectedFile), []byte(`{"sessions":["adm-1"],"explicit":true,"pinned":["adm-1"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	ss := []Session{
		{SessionID: "adm-1", Owner: e.svc.ownRoom()},
		{SessionID: "main-2", Owner: e.svc.ownRoom()},
		{SessionID: "t-1", Owner: "tiew"},
		{SessionID: "l-1", Owner: "lew"},
		{SessionID: "g-1", Owner: "gon"},
	}
	e.svc.markRoles(ss)
	got := []string{}
	for _, x := range ss {
		got = append(got, x.Role)
	}
	if want := []string{"admin", "", "frontend", "tester", ""}; !slices.Equal(got, want) {
		t.Fatalf("roles = %v, want %v", got, want)
	}
}

// regFile writes one Claude Code registry entry (an interactive terminal or
// VS Code session: no jobId) and its process.
func regFile(t *testing.T, e *env, home, pid, start, session string) {
	t.Helper()
	fakeProc(t, e.proc, pid, start)
	writeFile(t, filepath.Join(home, ".claude", "sessions", pid+".json"), `{"pid":`+pid+`,"sessionId":"`+session+`","cwd":"/x","procStart":"`+start+`","kind":"interactive","entrypoint":"claude-vscode","status":"idle"}`)
}

// TestAdminListFollowsAProcess: an interactive session has no jobId; after
// /clear the same Claude Code process (same pid and procStart) runs under a
// new session id. The pinned id is followed by process. A pid reused by a
// different process (procStart differs) is not followed, and a room's
// registry (another home) can never claim a listed id, by pid or by job.
func TestAdminListFollowsAProcess(t *testing.T) {
	e, _, _ := osUserEnv(t)
	e.svc.Duties = &RoomDuties{Path: filepath.Join(t.TempDir(), "room-duties.conf")}
	if _, err := e.store.Enable("t"); err != nil {
		t.Fatal(err)
	}
	room := filepath.Join(e.dir, "home", "tiew")
	e.col.Homes = []string{e.home, room}
	const pinned, other, claim = "aaaa1111-0000-4000-8000-000000000001", "bbbb2222-0000-4000-8000-000000000002", "cccc3333-0000-4000-8000-000000000003"
	if err := os.WriteFile(filepath.Join(e.store.dir, protectedFile), []byte(`{"sessions":["`+pinned+`","`+other+`","`+claim+`"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	snap := func() []Session { return e.col.Snapshot(context.Background()).Sessions }
	regFile(t, e, e.home, "4242", "777", pinned)
	regFile(t, e, e.home, "4343", "888", other)
	// a room writes a registry entry naming a listed id, under its own pid
	regFile(t, e, room, "5050", "999", claim)
	e.svc.protectionsFor(snap())
	l := readList(t, e.store.dir)
	if l.Procs[pinned] != "4242:777" || l.Procs[other] != "4343:888" {
		t.Fatalf("procs = %v", l.Procs)
	}
	if _, ok := l.Procs[claim]; ok {
		t.Fatal("a room's registry fed the process map")
	}

	// /clear in the pinned terminal: same process, new session id
	const cleared = "dddd4444-0000-4000-8000-000000000004"
	regFile(t, e, e.home, "4242", "777", cleared)
	// pid 4343 reused by a different process with another session
	const stranger = "eeee5555-0000-4000-8000-000000000005"
	regFile(t, e, e.home, "4343", "123", stranger)
	// the room moves its fake entry on, and also claims a pinned id's job
	const evil = "ffff6666-0000-4000-8000-000000000006"
	regFile(t, e, room, "5050", "999", evil)
	fakeProc(t, e.proc, "5151", "1")
	writeFile(t, filepath.Join(room, ".claude", "sessions", "5151.json"), `{"pid":5151,"sessionId":"`+evil+`x","procStart":"1","kind":"bg","jobId":"aaaa1111"}`)
	p := e.svc.protectionsFor(snap())
	l = readList(t, e.store.dir)
	if l.Aliases[pinned] != cleared || !slices.Contains(l.Sessions, cleared) || !p.covers(cleared) {
		t.Fatalf("not followed after /clear: %+v", l)
	}
	if slices.Contains(l.Sessions, stranger) || l.Aliases[other] != "" || p.covers(stranger) {
		t.Fatalf("a reused pid was followed: %+v", l)
	}
	if slices.Contains(l.Sessions, evil) || slices.Contains(l.Sessions, evil+"x") || l.Aliases[claim] != "" {
		t.Fatalf("a room claimed a listed id: %+v", l)
	}

	// the terminal closes: the alias stays (as for jobs)
	if err := os.Remove(filepath.Join(e.home, ".claude", "sessions", "4242.json")); err != nil {
		t.Fatal(err)
	}
	e.svc.protectionsFor(snap())
	if l = readList(t, e.store.dir); l.Aliases[pinned] != cleared {
		t.Fatalf("alias forgotten: %+v", l)
	}
}

// TestSessionAdminToggle: the admin pins one of the main service's own
// sessions and unpins it; a room's session or an unknown one is refused, a
// user token gets 403, and the request is never forwarded to a room.
func TestSessionAdminToggle(t *testing.T) {
	e, _, _ := osUserEnv(t)
	e.svc.Duties = &RoomDuties{Path: filepath.Join(t.TempDir(), "room-duties.conf")}
	admin, err := e.store.Enable("t")
	if err != nil {
		t.Fatal(err)
	}
	_, user, _ := e.store.AddUser("tiew", "tiew")
	room := filepath.Join(e.dir, "home", "tiew")
	e.col.Homes = []string{e.home, room}
	const mine, cleared, theirs = "aaaa1111-0000-4000-8000-000000000001", "dddd4444-0000-4000-8000-000000000004", "bbbb2222-0000-4000-8000-000000000002"
	regFile(t, e, e.home, "4242", "777", mine)
	regFile(t, e, room, "5050", "999", theirs)
	next := func() { e.svc.lastWrite = e.svc.lastWrite.Add(-writeSpacing) }
	path := apiPrefix + "/sessions/" + mine + "/admin"

	if code, _, _ := e.do(t, "POST", path, user, `{"on":true}`); code != 403 {
		t.Fatalf("a user token pinned a session: %d", code)
	}
	if code, _, body := e.do(t, "POST", path, admin, `{"on":true}`); code != 200 {
		t.Fatalf("pin = %d %s", code, body)
	}
	l := readList(t, e.store.dir)
	if !slices.Contains(l.Pinned, mine) || !slices.Contains(l.Sessions, mine) || l.Procs[mine] != "4242:777" {
		t.Fatalf("after pin: %+v", l)
	}
	if _, _, body := e.do(t, "GET", apiPrefix+"/snapshot", admin, ""); !strings.Contains(body, `"role":"admin","pinnable":true`) || strings.Count(body, `"pinnable":true`) != 1 {
		t.Fatalf("card badge (only the main service's own session is pinnable): %s", body)
	}
	// /clear, then unpin by the id the card shows now: the alias goes too
	regFile(t, e, e.home, "4242", "777", cleared)
	e.svc.syncAdminList(context.Background())
	if l = readList(t, e.store.dir); !slices.Contains(l.Sessions, cleared) {
		t.Fatalf("not followed: %+v", l)
	}
	next()
	if code, _, body := e.do(t, "POST", apiPrefix+"/sessions/"+cleared+"/admin", admin, `{"on":false}`); code != 200 {
		t.Fatalf("unpin = %d %s", code, body)
	}
	l = readList(t, e.store.dir)
	if slices.Contains(l.Pinned, mine) || slices.Contains(l.Sessions, mine) || slices.Contains(l.Sessions, cleared) || l.Aliases[mine] != "" {
		t.Fatalf("after unpin: %+v", l)
	}
	next()
	if code, _, _ := e.do(t, "POST", apiPrefix+"/sessions/"+theirs+"/admin", admin, `{"on":true}`); code != 409 {
		t.Fatalf("a room's session was pinned: %d", code)
	}
	next()
	if code, _, _ := e.do(t, "POST", apiPrefix+"/sessions/nosuch-1/admin", admin, `{"on":true}`); code != 404 {
		t.Fatalf("unknown session = %d", code)
	}
	if !localPath("/sessions/" + theirs + "/admin") {
		t.Fatal("the toggle must be answered by the main service, never forwarded to a room")
	}
	if l = readList(t, e.store.dir); slices.Contains(l.Sessions, theirs) {
		t.Fatalf("room session listed: %+v", l)
	}
}
