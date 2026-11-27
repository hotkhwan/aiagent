package world

import (
	"strings"
	"testing"
)

// The old connect code put a long-lived token in a QR
// (aiagent://connect?u=…&t=<token>); a photo of it, or another app that
// claimed the scheme, kept the key for good. The route is gone: every QR now
// carries a one-time pairing ticket (pair.go) or a login request (loginqr.go).
func TestTheConnectCodeRouteIsGone(t *testing.T) {
	e := newEnv(t)
	admin, _ := e.store.Enable("test")
	_, user, _ := e.store.AddUser("nok", "")
	body := `{"origin":"https://aiagent.joinanny.com"}`
	for _, tok := range []string{admin, user} {
		code, _, raw := pairCall(t, e, "/aiagent/api/connect/qr", tok, body, "")
		if code == 200 || strings.Contains(raw, "kaw_") || strings.Contains(raw, "PNG") {
			t.Fatalf("connect/qr still answers: %d %.80s", code, raw)
		}
	}
	// the admin can no longer draw another token into a QR either
	code, _, raw := pairCall(t, e, "/aiagent/api/connect/qr", admin, `{"origin":"https://aiagent.joinanny.com","token":"`+user+`"}`, "")
	if code == 200 || strings.Contains(raw, user) {
		t.Fatalf("connect/qr drew a user token: %d", code)
	}
}
