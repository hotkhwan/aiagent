package world

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// pairDevice pairs a phone through the page holding token and returns the
// token the phone received.
func pairDevice(t *testing.T, e *env, token string) string {
	t.Helper()
	_, ticket := issuePair(t, e, token)
	code, _, body := redeem(t, e, ticket, "")
	if code != 200 {
		t.Fatalf("redeem: %d %s", code, body)
	}
	return redeemedToken(t, body)
}

func idOf(t *testing.T, e *env, token string) (Identity, bool) {
	t.Helper()
	return e.store.Identify(token)
}

type deviceRow struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Owner     string `json:"owner"`
	Current   bool   `json:"current"`
	CreatedAt string `json:"createdAt"`
}

func listDevices(t *testing.T, e *env, token string) (int, []deviceRow) {
	t.Helper()
	code, _, raw := e.do(t, "GET", "/aiagent/api/connect/devices", token, "")
	var env struct {
		Details struct {
			Devices []deviceRow `json:"devices"`
		} `json:"details"`
	}
	_ = json.Unmarshal([]byte(raw), &env)
	return code, env.Details.Devices
}

// A phone gets a token of its own, never the page's: losing the phone then
// costs one revoke, not a rotation of the page's token. It has exactly the
// scope of the page that paired it.
func TestPairingGivesThePhoneItsOwnTokenWithThePagesScope(t *testing.T) {
	e := newEnv(t)
	admin, _ := e.store.Enable("test")
	_, user, _ := e.store.AddUser("nok", "")
	userID, _ := idOf(t, e, user)

	adminPhone := pairDevice(t, e, admin)
	userPhone := pairDevice(t, e, user)
	for _, c := range []struct {
		phone, page string
	}{{adminPhone, admin}, {userPhone, user}} {
		if c.phone == "" || c.phone == c.page {
			t.Fatal("the phone was given the page's own token")
		}
		if !strings.HasPrefix(c.phone, "kaw_") {
			t.Fatalf("device token shape: %q", c.phone[:4])
		}
	}
	if id, ok := idOf(t, e, adminPhone); !ok || id.Role != RoleAdmin || id.DeviceID == "" {
		t.Fatalf("admin phone = %+v %v", id, ok)
	}
	if id, ok := idOf(t, e, userPhone); !ok || id.Role != RoleUser || id.UserID != userID.UserID || id.Name != "nok" || id.DeviceID == "" {
		t.Fatalf("user phone = %+v %v", id, ok)
	}
	// only hashes are kept
	st, _ := e.store.Load()
	b, _ := json.Marshal(st)
	if strings.Contains(string(b), adminPhone) || strings.Contains(string(b), userPhone) {
		t.Fatal("a device token was stored in clear")
	}
}

// The device list: the admin sees every phone, a room's user only theirs, and
// each sees which entry is the phone asking.
func TestDeviceListIsScoped(t *testing.T) {
	e := newEnv(t)
	admin, _ := e.store.Enable("test")
	_, user, _ := e.store.AddUser("nok", "")
	adminPhone := pairDevice(t, e, admin)
	pairDevice(t, e, user)

	if code, rows := listDevices(t, e, admin); code != 200 || len(rows) != 2 {
		t.Fatalf("admin sees %d %+v", code, rows)
	}
	code, rows := listDevices(t, e, user)
	if code != 200 || len(rows) != 1 || rows[0].Owner != "nok" {
		t.Fatalf("user sees %d %+v", code, rows)
	}
	_, rows = listDevices(t, e, adminPhone)
	current := 0
	for _, r := range rows {
		if r.Current {
			current++
			if r.Owner != "admin" {
				t.Fatalf("current row: %+v", r)
			}
		}
	}
	if current != 1 {
		t.Fatalf("the asking phone is marked %d times", current)
	}
	if code, _ := listDevices(t, e, ""); code == 200 {
		t.Fatal("the list was answered without a token")
	}
}

// Revoking a phone ends its token at once and nothing else; a user cannot
// revoke a phone outside their room.
func TestRevokingADeviceEndsOnlyThatPhone(t *testing.T) {
	e := newEnv(t)
	admin, _ := e.store.Enable("test")
	_, user, _ := e.store.AddUser("nok", "")
	phone1 := pairDevice(t, e, admin)
	phone2 := pairDevice(t, e, admin)
	userPhone := pairDevice(t, e, user)
	_, rows := listDevices(t, e, phone1)
	var id1 string
	for _, r := range rows {
		if r.Current {
			id1 = r.ID
		}
	}
	if code, _, raw := e.do(t, "POST", "/aiagent/api/connect/devices/"+id1+"/revoke", user, "{}"); code == 200 {
		t.Fatalf("a user revoked the admin's phone: %s", raw)
	}
	e.svc.lastWrite = time.Time{} // writes are spaced a second apart
	if code, _, raw := e.do(t, "POST", "/aiagent/api/connect/devices/"+id1+"/revoke", admin, "{}"); code != 200 {
		t.Fatalf("revoke: %d %s", code, raw)
	}
	if _, ok := idOf(t, e, phone1); ok {
		t.Fatal("a revoked phone still opens the service")
	}
	for _, tok := range []string{phone2, userPhone, admin, user} {
		if _, ok := idOf(t, e, tok); !ok {
			t.Fatal("revoking one phone ended another token")
		}
	}
}

// Replacing a token because it may have leaked must also end the phones that
// whoever held it could have paired.
func TestReplacingATokenEndsThePhonesPairedUnderIt(t *testing.T) {
	e := newEnv(t)
	admin, _ := e.store.Enable("test")
	u, user, _ := e.store.AddUser("nok", "")
	_, other, _ := e.store.AddUser("som", "")
	adminPhone := pairDevice(t, e, admin)
	userPhone := pairDevice(t, e, user)
	otherPhone := pairDevice(t, e, other)

	if _, err := e.store.Enable("rotate"); err != nil {
		t.Fatal(err)
	}
	if _, ok := idOf(t, e, adminPhone); ok {
		t.Fatal("an admin phone survived rotate-token")
	}
	if _, ok := idOf(t, e, userPhone); !ok {
		t.Fatal("rotating the admin token ended a room's phone")
	}
	if _, _, err := e.store.ReissueUser(u.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := idOf(t, e, userPhone); ok {
		t.Fatal("a room's phone survived a reissue of its token")
	}
	if _, ok := idOf(t, e, otherPhone); !ok {
		t.Fatal("reissuing one room ended another room's phone")
	}
	o, _ := idOf(t, e, other)
	if err := e.store.RevokeUser(o.UserID); err != nil {
		t.Fatal(err)
	}
	if _, ok := idOf(t, e, otherPhone); ok {
		t.Fatal("a phone outlived its room")
	}
}

// A phone cannot pair further phones: a stolen phone must not be able to mint
// tokens that outlive its own revoke. Pairing is done from a page's token.
func TestAPhoneCannotPairAnotherPhone(t *testing.T) {
	e := newEnv(t)
	admin, _ := e.store.Enable("test")
	phone := pairDevice(t, e, admin)
	code, _, body := pairCall(t, e, "/aiagent/api/connect/pair", phone, `{"origin":"https://aiagent.joinanny.com"}`, "")
	if code == 200 {
		t.Fatalf("a phone drew a pairing code: %s", body)
	}
}
