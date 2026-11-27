package world

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
)

// ---------- F1: a user's text never reaches the CLI as a command ----------

// Without --os-users the user's text went straight to SendToSession, which
// only cleans it: "!cat ~/.ssh/id" opened the CLI's shell mode for a user
// token. The check now runs in the gate, in both modes, for every route that
// carries typed text into an agent.
func TestUserCommandsAreRefusedWithoutOSUsers(t *testing.T) {
	e := newEnv(t)
	admin, _ := e.store.Enable("test")
	_, user, _ := e.store.AddUser("nok", "")
	const sid = "0a1b2c3d-0000-4000-8000-00000000beef"
	bad := []string{"!cat ~/.ssh/x", "/login", "/model haiku", "  !x", "\u200b!ls", "\ufeff/config", "\u0301!ls", "& run in cloud", "/home/x/.ssh/id อ่านให้หน่อย", "/tmp/x", "\uff01ls", "\uff0flogin"}
	for _, m := range bad {
		b, _ := json.Marshal(map[string]string{"message": m})
		ws, _ := json.Marshal(map[string]string{"workspace": "api", "message": m})
		ask, _ := json.Marshal(map[string]any{"ask": map[string]any{"tabs": []string{"Q"}, "answers": []map[string]any{{"tab": 0, "question": "q?", "text": m}}}})
		for _, r := range []struct{ path, body string }{
			{"/sessions/" + sid + "/message", string(b)},
			{"/messages", string(ws)},
			{"/sessions/" + sid + "/answer", string(ask)},
		} {
			e.svc.lastWrite = time.Time{}
			code, m2, raw := e.do(t, "POST", apiPrefix+r.path, user, r.body)
			if code != 403 || m2["code"] != "AIAGENT_COMMAND_BLOCKED" {
				t.Errorf("user %s %q = %d %s, want 403 AIAGENT_COMMAND_BLOCKED", r.path, m, code, raw)
			}
		}
	}
	// the admin is not held to it
	for _, m := range []string{"!ls", "/model haiku"} {
		e.svc.lastWrite = time.Time{}
		b, _ := json.Marshal(map[string]string{"message": m})
		if _, m2, raw := e.do(t, "POST", apiPrefix+"/sessions/"+sid+"/message", admin, string(b)); m2["code"] == "AIAGENT_COMMAND_BLOCKED" {
			t.Errorf("admin %q was blocked: %s", m, raw)
		}
	}
	// plain text and the allowed commands still go through the gate
	for _, m := range []string{"ช่วยดู /home/x/file หน่อย", "/compact", "/rename งานใหม่", "รูปแนบ 1: /home/dev/.aiagent/uploads/x.png"} {
		e.svc.lastWrite = time.Time{}
		b, _ := json.Marshal(map[string]string{"message": m})
		if _, m2, raw := e.do(t, "POST", apiPrefix+"/sessions/"+sid+"/message", user, string(b)); m2["code"] == "AIAGENT_COMMAND_BLOCKED" {
			t.Errorf("user %q was blocked: %s", m, raw)
		}
	}
}

// A message that starts with a path is refused for users: the CLI may read
// any leading "/word" as a command. The web always puts a label first.
func TestUserTextRefusesEveryLeadingSlashButTheAllowedCommands(t *testing.T) {
	for _, m := range []string{"/home/x/docs อ่านให้หน่อย", "/tmp/a", "/home/", " /tmp/x"} {
		if userText(m) {
			t.Errorf("%q was let through", m)
		}
	}
	for _, m := range []string{"/compact", "/clear", "/context", "/usage", "/rename x", "อ่าน /home/x/docs"} {
		if !userText(m) {
			t.Errorf("%q was blocked", m)
		}
	}
}

// ---------- F2: what a paired phone's token may do ----------

func TestAdminPhoneTokenCannotManageTheMachine(t *testing.T) {
	e := newEnv(t)
	admin, _ := e.store.Enable("test")
	phone := pairDevice(t, e, admin)
	for _, p := range []struct{ m, path string }{
		{"GET", "/vault"}, {"POST", "/vault"}, {"POST", "/vault/x/remove"}, {"GET", "/update"}, {"POST", "/update"}, {"GET", "/update/release"},
		{"POST", "/clis/update"}, {"GET", "/accounts"}, {"POST", "/accounts/claude/login"}, {"POST", "/accounts/claude/logout"},
		{"GET", "/policy"}, {"POST", "/policy"}, {"POST", "/policy/idle"}, {"POST", "/rooms"}, {"POST", "/codex/model"},
		{"GET", "/environments"}, {"POST", "/environments"}, {"POST", "/environments/x/remove"}, {"POST", "/connect/pair"},
	} {
		e.svc.lastWrite = time.Time{}
		if code, _, _ := e.do(t, p.m, apiPrefix+p.path, phone, `{}`); code != 403 {
			t.Errorf("phone %s %s = %d, want 403", p.m, p.path, code)
		}
	}
	for _, p := range []string{"/snapshot", "/me", "/reviews", "/tokens", "/connect/devices", "/faces"} {
		if code, _, raw := e.do(t, "GET", apiPrefix+p, phone, ""); code != 200 {
			t.Errorf("phone GET %s = %d %s", p, code, raw)
		}
	}
	// the page's own token still may
	if code, _, _ := e.do(t, "GET", apiPrefix+"/vault", admin, ""); code == 403 {
		t.Error("admin web token was refused the vault")
	}
}

// A token an admin phone makes or reissues dies with the phone: revoking a
// stolen phone must not leave behind credentials it minted.
func TestTokensAPhoneMadeEndWithThePhone(t *testing.T) {
	e := newEnv(t)
	admin, _ := e.store.Enable("test")
	phone := pairDevice(t, e, admin)
	other := pairDevice(t, e, admin)
	_, webMade, _ := e.store.AddUser("web-made", "")

	e.svc.lastWrite = time.Time{}
	code, m, raw := e.do(t, "POST", apiPrefix+"/tokens", phone, `{"name":"phone-made"}`)
	if code != 200 {
		t.Fatalf("phone create = %d %s", code, raw)
	}
	made := m["details"].(map[string]any)["token"].(string)
	// a token made on the web and reissued from the phone is the phone's now
	st, _ := e.store.Load()
	var webID string
	for _, u := range st.Users {
		if u.Name == "web-made" {
			webID = u.ID
		}
	}
	e.svc.lastWrite = time.Time{}
	code, m, raw = e.do(t, "POST", apiPrefix+"/tokens/"+webID+"/reissue", phone, `{}`)
	if code != 200 {
		t.Fatalf("phone reissue = %d %s", code, raw)
	}
	reissued := m["details"].(map[string]any)["token"].(string)
	if _, ok := e.store.Identify(webMade); ok {
		t.Fatal("the old token outlived its reissue")
	}
	// a phone paired under the phone-made token goes with it
	madeDev := pairDevice(t, e, made)
	_, kept, _ := e.store.AddUser("kept", "")

	me, _ := e.store.Identify(phone)
	e.svc.lastWrite = time.Time{}
	if code, _, raw := e.do(t, "POST", apiPrefix+"/connect/devices/"+me.DeviceID+"/revoke", admin, `{}`); code != 200 {
		t.Fatalf("revoke = %d %s", code, raw)
	}
	for name, tok := range map[string]string{"phone": phone, "made": made, "reissued": reissued, "made's phone": madeDev} {
		if _, ok := e.store.Identify(tok); ok {
			t.Errorf("%s token still works after the phone was revoked", name)
		}
	}
	for name, tok := range map[string]string{"other phone": other, "kept": kept, "admin": admin} {
		if _, ok := e.store.Identify(tok); !ok {
			t.Errorf("%s token was ended with someone else's phone", name)
		}
	}
	// the rows stay, so the admin can reissue them from the web
	st, _ = e.store.Load()
	if len(st.Users) != 3 {
		t.Fatalf("users = %+v", st.Users)
	}
	for _, u := range st.Users {
		if u.CreatedByDevice != "" {
			t.Errorf("%s is still tied to a revoked phone", u.Name)
		}
	}
}

// Rotating the admin token ends the admin's phones, and with them the user
// tokens they made.
func TestRotationEndsTheTokensAdminPhonesMade(t *testing.T) {
	e := newEnv(t)
	admin, _ := e.store.Enable("test")
	phone := pairDevice(t, e, admin)
	e.svc.lastWrite = time.Time{}
	code, m, raw := e.do(t, "POST", apiPrefix+"/tokens", phone, `{"name":"phone-made"}`)
	if code != 200 {
		t.Fatalf("phone create = %d %s", code, raw)
	}
	made := m["details"].(map[string]any)["token"].(string)
	_, web, _ := e.store.AddUser("web-made", "")
	if _, err := e.store.Enable("test"); err != nil {
		t.Fatal(err)
	}
	if _, ok := e.store.Identify(made); ok {
		t.Fatal("a token an admin phone made outlived the rotation that ended the phone")
	}
	if _, ok := e.store.Identify(web); !ok {
		t.Fatal("rotation ended a token made on the web")
	}
}

// ---------- F3: lockout ----------

// Filling the table with fresh addresses used to wipe it, unlocking the
// guesser. Now the oldest entries go first.
func TestTheFailureTableEvictsTheOldestInsteadOfWiping(t *testing.T) {
	s := &Service{failures: map[string][]time.Time{}}
	t0 := time.Now().Add(-time.Minute)
	for i := range maxFailureKeys {
		s.recordFailure("k"+strconv.Itoa(i), t0)
	}
	now := time.Now()
	for range maxFailures {
		s.recordFailure("guesser", now)
	}
	s.recordFailure("fresh", now)
	if !s.tooManyFailures("guesser", now) {
		t.Fatal("the guesser was unlocked by a full table")
	}
	if len(s.failures) > maxFailureKeys {
		t.Fatalf("table grew to %d", len(s.failures))
	}

	p := &pairState{}
	for i := range maxFailureKeys {
		p.failed("k"+strconv.Itoa(i), t0)
	}
	for range pairMaxFailures {
		p.failed("guesser", now)
	}
	p.failed("fresh", now)
	if !p.limited("guesser", now) {
		t.Fatal("the pairing guesser was unlocked by a full table")
	}
}

// ---------- F4: one identity cannot starve another ----------

func TestOneUsersWritesDoNotSlowTheAdminOrAnotherUser(t *testing.T) {
	e := newEnv(t)
	admin, _ := e.store.Enable("test")
	_, a, _ := e.store.AddUser("a", "")
	_, b, _ := e.store.AddUser("b", "")
	const name = `{"name":"x"}`
	path := apiPrefix + "/sessions/0a1b2c3d-0000-4000-8000-00000000beef/name"
	if code, _, raw := e.do(t, "POST", path, a, name); code != 200 {
		t.Fatalf("a = %d %s", code, raw)
	}
	if code, _, raw := e.do(t, "POST", path, admin, name); code == 429 {
		t.Fatalf("admin was slowed by a user: %s", raw)
	}
	if code, _, raw := e.do(t, "POST", path, b, name); code == 429 {
		t.Fatalf("user b was slowed by user a: %s", raw)
	}
	if code, _, _ := e.do(t, "POST", path, a, name); code != 429 {
		t.Fatalf("a's own second write in a second = %d, want 429", code)
	}
}

func TestAUserCannotFillThePhoneSlots(t *testing.T) {
	e := newEnv(t)
	admin, _ := e.store.Enable("test")
	u, _, _ := e.store.AddUser("a", "")
	who := Identity{Role: RoleUser, UserID: u.ID}
	n := 0
	for ; n < maxDevices+1; n++ {
		if _, _, err := e.store.AddDevice(who, "p"); err != nil {
			break
		}
	}
	if n != maxDevicesPerUser {
		t.Fatalf("a user paired %d phones, want %d", n, maxDevicesPerUser)
	}
	if _, _, err := e.store.AddDevice(Identity{Role: RoleAdmin}, "mine"); err != nil {
		t.Fatalf("the admin could not pair after a user filled theirs: %v", err)
	}

	// push slots: users together cannot take the admin's
	for i := 0; i < pushMaxDevices/pushPerToken+1; i++ {
		_, tok, err := e.store.AddUser("u"+strconv.Itoa(i), "")
		if err != nil {
			t.Fatal(err)
		}
		for j := range pushPerToken {
			_ = e.svc.addPushDevice("aph_"+strings.Repeat("x", 40)+itoa3(i*pushPerToken+j), tok)
		}
	}
	if err := e.svc.addPushDevice(handleA, admin); err != nil {
		t.Fatalf("the admin's phone could not register: %v", err)
	}
}

// ---------- F5: a session or review id claimed by two rooms ----------

func TestAnIDTwoRoomsClaimRoutesToNeither(t *testing.T) {
	e, _, o := osUserEnv(t)
	_, _ = e.store.Enable("t")
	_, _, _ = e.store.AddUser("vibe-a", "vibe-a")
	_, _, _ = e.store.AddUser("vibe-b", "vibe-b")
	const dupSession, dupReview = "dup00000-0000-4000-8000-000000000000", "r20261101-101010-deadbeef"
	room := func(own string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch r.URL.Path {
			case apiPrefix + "/snapshot":
				_, _ = w.Write([]byte(`{"code":"SUCCESS","details":{"sessions":[{"sessionId":"` + dupSession + `"},{"sessionId":"` + own + `"}]}}`))
			case apiPrefix + "/reviews":
				_, _ = w.Write([]byte(`{"code":"SUCCESS","details":{"reviews":[{"id":"` + dupReview + `","title":"x","status":"open"}]}}`))
			}
		}
	}
	instance(t, o, "vibe-a", room("aaaa0000-0000-4000-8000-000000000000"))
	instance(t, o, "vibe-b", room("bbbb0000-0000-4000-8000-000000000000"))
	ctx := context.Background()
	if got := e.svc.teammateOf(ctx, "", dupSession); got != "" {
		t.Fatalf("a session both rooms claim went to %q", got)
	}
	if got := e.svc.teammateOf(ctx, "", "aaaa0000-0000-4000-8000-000000000000"); got != "vibe-a" {
		t.Fatalf("a room's own session = %q", got)
	}
	if got := e.svc.roomOfReview(ctx, dupReview); got != "" {
		t.Fatalf("a review both rooms claim went to %q", got)
	}
	list, _ := e.svc.roomReviews(ctx)
	if len(list) != 0 {
		t.Fatalf("a claimed-twice review is listed: %+v", list)
	}
}

// ---------- F6: runs in protected workspaces ----------

func TestUsersDoNotSeeRunsInProtectedWorkspaces(t *testing.T) {
	e := newEnv(t)
	e.col.Workspaces = append(e.col.Workspaces, workspaceEntry{name: "secret", session: "secret", dir: "/home/dev/secret", protected: true})
	svc := &Service{Store: e.store, Collector: e.col}
	snap := Snapshot{Runs: []Run{{ID: "r1", Task: "SECRET task", Workspace: "secret"}, {ID: "r2", Task: "open task", Workspace: "api"}}}
	b, _ := json.Marshal(svc.forRole(Identity{Role: RoleUser}, snap).Runs)
	if strings.Contains(string(b), "SECRET") || !strings.Contains(string(b), "open task") {
		t.Fatalf("user runs = %s", b)
	}
	if got := svc.forRole(Identity{Role: RoleAdmin}, snap).Runs; len(got) != 2 {
		t.Fatalf("admin runs = %+v", got)
	}
}

// ---------- F7: a quiet phone gets no names ----------

func TestAQuietPhoneIsSentNoNames(t *testing.T) {
	e := newEnv(t)
	admin, _ := e.store.Enable("test")
	relay := newFakeRelay(t)
	e.svc.PushRelay = relay.srv.URL
	items := []PushItem{}
	e.svc.pushSource = func(context.Context, Identity) ([]PushItem, error) { return items, nil }
	e.svc.lastWrite = time.Time{}
	if code, _, raw := e.do(t, "POST", apiPrefix+"/push/devices", admin, `{"handle":"`+handleA+`","quiet":true}`); code != 200 {
		t.Fatalf("register = %d %s", code, raw)
	}
	e.svc.lastWrite = time.Time{}
	if code, _, raw := e.do(t, "POST", apiPrefix+"/push/devices", admin, `{"handle":"`+handleB+`"}`); code != 200 {
		t.Fatalf("register = %d %s", code, raw)
	}
	ctx := context.Background()
	e.svc.PushTick(ctx)
	items = []PushItem{{Kind: "review", Key: "r|1", Name: "SECRET title", Ref: "r1"}}
	e.svc.PushTick(ctx)
	got := relay.take()
	if len(got) != 2 {
		t.Fatalf("sent %v", got)
	}
	for _, m := range got {
		_, has := m["name"]
		switch m["handle"] {
		case handleA:
			if m["name"] != "" || !has || m["kind"] != "review" || m["ref"] != "r1" {
				t.Errorf("quiet phone got %v", m)
			}
		case handleB:
			if m["name"] != "SECRET title" {
				t.Errorf("a phone that did not ask got %v", m)
			}
		}
	}
}

// ---------- F9: optional Host allowlist ----------

func TestAllowedHostsWhenSet(t *testing.T) {
	e := newEnv(t)
	tok, _ := e.store.Enable("test")
	get := func(host string) int {
		req := httptest.NewRequest("GET", "/aiagent/api/snapshot", nil)
		req.Host = host
		req.Header.Set(TokenHeader, tok)
		resp, err := e.app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp.StatusCode
	}
	if code := get("evil.example"); code != 200 {
		t.Fatalf("off by default: %d", code)
	}
	e.svc.AllowedHosts = []string{"aiagent.joinanny.com"}
	for host, want := range map[string]int{"evil.example": 404, "aiagent.joinanny.com": 200, "AIAGENT.joinanny.com:443": 200, "localhost:8890": 200, "127.0.0.1:8890": 200, "[::1]:8890": 200} {
		if code := get(host); code != want {
			t.Errorf("Host %s = %d, want %d", host, code, want)
		}
	}
	hosts, err := ParseAllowedHosts(" a.example , B.example:443,, ")
	if err != nil || strings.Join(hosts, ",") != "a.example,b.example" {
		t.Fatalf("parse = %v %v", hosts, err)
	}
	if _, err := ParseAllowedHosts("a b"); err == nil {
		t.Fatal("a host with a space was accepted")
	}
}

// ---------- helpers ----------

func listenLoopback(t *testing.T, e *env) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = e.app.Listener(ln, fiber.ListenConfig{DisableStartupMessage: true}) }()
	t.Cleanup(func() { _ = e.app.Shutdown() })
	return ln.Addr().String()
}

// itoa3 is i as three base64url characters, to finish a push handle.
func itoa3(i int) string {
	const a = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	return string([]byte{a[(i/4096)%64], a[(i/64)%64], a[i%64]})
}
