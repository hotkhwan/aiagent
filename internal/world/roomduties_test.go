package world

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRoomDutiesFile: Set writes sorted room=duty lines, "" removes a room,
// hand-written junk is ignored, and a bad duty or room name is refused.
func TestRoomDutiesFile(t *testing.T) {
	d := &RoomDuties{Path: filepath.Join(t.TempDir(), "room-duties.conf")}
	if got := d.Load(); len(got) != 0 {
		t.Fatalf("missing file = %v", got)
	}
	for room, duty := range map[string]string{"tiew": DutyFrontend, "lew": DutyTester, "khwan": DutyBackend} {
		if err := d.Set(room, duty); err != nil {
			t.Fatal(err)
		}
	}
	if err := d.Set("khwan", ""); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(d.Path)
	if !strings.HasSuffix(string(b), "lew=tester\ntiew=frontend\n") || strings.Contains(string(b), "khwan") {
		t.Fatalf("file = %q", b)
	}
	if err := os.WriteFile(d.Path, append(b, []byte("bad line\nx=admin\nGon=frontend\n  gon = frontend  \n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	got := d.Load()
	if len(got) != 3 || got["gon"] != DutyFrontend || got["lew"] != DutyTester {
		t.Fatalf("load = %v", got)
	}
	if d.Set("gon", "admin") == nil || d.Set("../x", DutyTester) == nil {
		t.Fatal("a bad duty or room name was accepted")
	}
	var none *RoomDuties
	if none.Set("gon", DutyTester) != nil || len(none.Load()) != 0 {
		t.Fatal("no duties file must be a no-op")
	}
}

// TestTokenDuty: the admin picks a duty when creating a room's token, can
// change it, the token list shows it, revoking the token drops it, and a user
// token cannot set one.
func TestTokenDuty(t *testing.T) {
	e, _, _ := osUserEnv(t)
	e.svc.Duties = &RoomDuties{Path: filepath.Join(t.TempDir(), "room-duties.conf")}
	admin, err := e.store.Enable("t")
	if err != nil {
		t.Fatal(err)
	}
	next := func() { e.svc.lastWrite = e.svc.lastWrite.Add(-writeSpacing) }
	if code, _, _ := e.do(t, "POST", apiPrefix+"/tokens", admin, `{"name":"vibe-b","duty":"boss"}`); code != 400 {
		t.Fatalf("unknown duty = %d", code)
	}
	next()
	code, m, body := e.do(t, "POST", apiPrefix+"/tokens", admin, `{"name":"vibe-b","duty":"frontend"}`)
	if code != 200 {
		t.Fatalf("create = %d %s", code, body)
	}
	if got := e.svc.Duties.Load()["vibe-b"]; got != DutyFrontend {
		t.Fatalf("duty after create = %q", got)
	}
	id := m["details"].(map[string]any)["id"].(string)
	user := m["details"].(map[string]any)["token"].(string)
	next()
	if code, _, body := e.do(t, "POST", apiPrefix+"/tokens/"+id+"/duty", admin, `{"duty":"tester"}`); code != 200 {
		t.Fatalf("set duty = %d %s", code, body)
	}
	if _, _, body := e.do(t, "GET", apiPrefix+"/tokens", admin, ""); !strings.Contains(body, `"duty":"tester"`) {
		t.Fatalf("list = %s", body)
	}
	next()
	if code, _, _ := e.do(t, "POST", apiPrefix+"/tokens/"+id+"/duty", user, `{"duty":"frontend"}`); code != 403 {
		t.Fatalf("a user set a duty: %d", code)
	}
	next()
	if code, _, body := e.do(t, "POST", apiPrefix+"/tokens/"+id+"/revoke", admin, ""); code != 200 {
		t.Fatalf("revoke = %d %s", code, body)
	}
	if got := e.svc.Duties.Load(); len(got) != 0 {
		t.Fatalf("duties after revoke = %v", got)
	}
}
