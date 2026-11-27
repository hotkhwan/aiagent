package world

import (
	"encoding/json"
	"net/url"
	"os"
	"strings"
	"testing"
)

// tokenPair asks for a pairing code for user token id, proving it holds it.
func tokenPair(t *testing.T, e *env, caller, id, userToken string) (int, string) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"token": userToken, "origin": "https://aiagent.joinanny.com"})
	code, _, raw := pairCall(t, e, "/aiagent/api/tokens/"+id+"/pair", caller, string(body), "")
	return code, raw
}

// An admin who has just made (or reissued) a user token can hand it over as a
// one-time pairing QR instead of the token itself; the phone that scans it
// gets a device token in that user's scope.
func TestAdminMintsAPairingCodeForAUserToken(t *testing.T) {
	e := newEnv(t)
	admin, _ := e.store.Enable("test")
	u, user, _ := e.store.AddUser("nok", "")

	code, raw := tokenPair(t, e, admin, u.ID, user)
	if code != 200 {
		t.Fatalf("pair: %d %s", code, raw)
	}
	var env struct {
		Details pairIssued `json:"details"`
	}
	_ = json.Unmarshal([]byte(raw), &env)
	if strings.Contains(raw, user) {
		t.Fatal("the reply repeats the token")
	}
	pu, err := url.Parse(env.Details.Code)
	if err != nil || pu.Host != "pair" || !strings.HasPrefix(env.Details.QR, "data:image/png;base64,") {
		t.Fatalf("code = %q", env.Details.Code)
	}
	rc, _, body := redeem(t, e, pu.Query().Get("c"), "")
	if rc != 200 {
		t.Fatalf("redeem: %d %s", rc, body)
	}
	phone := redeemedToken(t, body)
	id, ok := e.store.Identify(phone)
	if !ok || id.Role != RoleUser || id.UserID != u.ID || id.DeviceID == "" || phone == user {
		t.Fatalf("phone = %+v %v", id, ok)
	}
	b, _ := os.ReadFile(e.auditFP)
	if !strings.Contains(string(b), "aiagent.tokenPair") || strings.Contains(string(b), user) {
		t.Fatalf("audit: %s", b)
	}
}

// A user token cannot mint codes, and the admin must present the very token
// of the user it names: never another user's, never the admin's own.
func TestUserCannotMintPairingCodesForOthers(t *testing.T) {
	e := newEnv(t)
	admin, _ := e.store.Enable("test")
	u, user, _ := e.store.AddUser("nok", "")
	u2, user2, _ := e.store.AddUser("pim", "")

	if code, _ := tokenPair(t, e, user, u2.ID, user2); code != 403 {
		t.Fatalf("user minting: %d", code)
	}
	if code, _ := tokenPair(t, e, "", u.ID, user); code != 401 {
		t.Fatalf("no token: %d", code)
	}
	if code, _ := tokenPair(t, e, admin, u.ID, user2); code != 404 {
		t.Fatalf("another user's token: %d", code)
	}
	if code, _ := tokenPair(t, e, admin, u.ID, admin); code != 404 {
		t.Fatalf("admin's own token: %d", code)
	}
	if code, _ := tokenPair(t, e, admin, u.ID, "kaw_"+strings.Repeat("z", 43)); code != 404 {
		t.Fatalf("unknown token: %d", code)
	}
	// a reissued token's old value opens nothing
	if _, _, err := e.store.ReissueUser(u.ID); err != nil {
		t.Fatal(err)
	}
	if code, _ := tokenPair(t, e, admin, u.ID, user); code != 404 {
		t.Fatalf("stale token: %d", code)
	}
}
