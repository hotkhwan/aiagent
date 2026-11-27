package world

import (
	"encoding/json"
	"io"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

// loginCall makes one request with optional token, login nonce and visitor
// address, and returns the status and the raw body.
func loginCall(t *testing.T, e *env, method, path, token, nonce, body, ip string) (int, string) {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, r)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set(TokenHeader, token)
	}
	if nonce != "" {
		req.Header.Set(LoginNonceHeader, nonce)
	}
	if ip != "" {
		req.Header.Set("Cf-Connecting-Ip", ip)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh) Safari/605.1.15")
	resp, err := e.app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

type loginStarted struct {
	ID, Nonce, Secret string
}

// startLogin asks for a login QR as a browser with no token would and reads
// the secret back out of the code the QR carries.
func startLogin(t *testing.T, e *env, ip string) loginStarted {
	t.Helper()
	code, body := loginCall(t, e, "POST", "/aiagent/login-qr", "", "", `{"origin":"https://aiagent.joinanny.com"}`, ip)
	if code != 200 {
		t.Fatalf("create: %d %s", code, body)
	}
	var env struct {
		Details struct {
			ID        string `json:"id"`
			Nonce     string `json:"nonce"`
			Code      string `json:"code"`
			QR        string `json:"qr"`
			ExpiresAt string `json:"expiresAt"`
		} `json:"details"`
	}
	if err := json.Unmarshal([]byte(body), &env); err != nil {
		t.Fatal(err)
	}
	d := env.Details
	if !strings.HasPrefix(d.QR, "data:image/png;base64,") || d.Nonce == "" || d.ID == "" {
		t.Fatalf("create body: %s", body)
	}
	exp, err := time.Parse(time.RFC3339, d.ExpiresAt)
	if err != nil || time.Until(exp) > loginQRTTL+time.Second || time.Until(exp) < loginQRTTL-10*time.Second {
		t.Fatalf("expiresAt %q", d.ExpiresAt)
	}
	u, err := url.Parse(d.Code)
	if err != nil || u.Scheme != "aiagent" || u.Host != "login" || u.Query().Get("i") != d.ID || u.Query().Get("u") != "https://aiagent.joinanny.com" {
		t.Fatalf("code = %q", d.Code)
	}
	// the nonce never goes into the QR: a photo of it cannot collect the token
	if strings.Contains(d.Code, d.Nonce) {
		t.Fatal("the nonce is in the QR")
	}
	return loginStarted{ID: d.ID, Nonce: d.Nonce, Secret: u.Query().Get("s")}
}

func pollLogin(t *testing.T, e *env, l loginStarted, nonce, ip string) (int, string, string) {
	t.Helper()
	code, body := loginCall(t, e, "GET", "/aiagent/login-qr/"+l.ID, "", nonce, "", ip)
	var env struct {
		Details struct {
			Status string `json:"status"`
			Token  string `json:"token"`
		} `json:"details"`
	}
	_ = json.Unmarshal([]byte(body), &env)
	return code, env.Details.Status, env.Details.Token
}

func approveLogin(t *testing.T, e *env, l loginStarted, token string) (int, string) {
	t.Helper()
	e.svc.lastWrite = time.Time{} // one write a second is not what these test
	return loginCall(t, e, "POST", "/aiagent/api/login-qr/"+l.ID+"/approve", token, "", `{"secret":"`+l.Secret+`"}`, "")
}

// The whole round: a browser shows a QR, a signed-in app approves it, and the
// browser that holds the nonce receives a token of its own, once.
func TestLoginQRGivesTheWaitingBrowserItsOwnTokenOnce(t *testing.T) {
	e := newEnv(t)
	admin, _ := e.store.Enable("test")
	l := startLogin(t, e, "198.51.100.7")

	if code, status, tok := pollLogin(t, e, l, l.Nonce, "198.51.100.7"); code != 200 || status != "pending" || tok != "" {
		t.Fatalf("before approval: %d %q %q", code, status, tok)
	}
	// the app may see who is asking before it approves
	code, body := loginCall(t, e, "POST", "/aiagent/api/login-qr/"+l.ID+"/info", admin, "", `{"secret":"`+l.Secret+`"}`, "")
	if code != 200 || !strings.Contains(body, "Safari") || !strings.Contains(body, `"address":`) || !strings.Contains(body, "https://aiagent.joinanny.com") {
		t.Fatalf("info: %d %s", code, body)
	}
	if code, body := approveLogin(t, e, l, admin); code != 200 {
		t.Fatalf("approve: %d %s", code, body)
	}
	code, status, tok := pollLogin(t, e, l, l.Nonce, "198.51.100.7")
	if code != 200 || status != "approved" || !strings.HasPrefix(tok, "kaw_") {
		t.Fatalf("after approval: %d %q", code, status)
	}
	if tok == admin {
		t.Fatal("the browser was given the approver's own token")
	}
	id, ok := e.store.Identify(tok)
	if !ok || id.Role != RoleAdmin || id.DeviceID == "" {
		t.Fatalf("browser token = %+v %v", id, ok)
	}
	// single use: a second poll, even with the right nonce, gets nothing
	if code, _, tok2 := pollLogin(t, e, l, l.Nonce, "198.51.100.7"); code != 404 || tok2 != "" {
		t.Fatalf("second poll: %d", code)
	}
	// and the approval cannot be replayed
	if code, _ := approveLogin(t, e, l, admin); code != 404 {
		t.Fatalf("second approve: %d", code)
	}
	// on record, without the token
	b, _ := os.ReadFile(e.auditFP)
	if !strings.Contains(string(b), "aiagent.loginQRApprove") || !strings.Contains(string(b), "aiagent.loginQR") {
		t.Fatalf("audit: %s", b)
	}
	if strings.Contains(string(b), tok) || strings.Contains(string(b), l.Secret) || strings.Contains(string(b), l.Nonce) {
		t.Fatal("a secret reached the audit log")
	}
	// it is in the devices list and can be revoked there
	_, rows := listDevices(t, e, admin)
	found := ""
	for _, r := range rows {
		if r.ID == id.DeviceID {
			found = r.ID
		}
	}
	if found == "" {
		t.Fatalf("browser not listed: %+v", rows)
	}
	e.svc.lastWrite = time.Time{}
	if code, _, body := e.do(t, "POST", "/aiagent/api/connect/devices/"+found+"/revoke", admin, "{}"); code != 200 {
		t.Fatalf("revoke: %d %s", code, body)
	}
	if _, ok := e.store.Identify(tok); ok {
		t.Fatal("a revoked browser token still works")
	}
}

// A photographed QR alone (id and secret, no nonce) never receives the token.
func TestLoginQRWrongNonceGetsNothing(t *testing.T) {
	e := newEnv(t)
	admin, _ := e.store.Enable("test")
	l := startLogin(t, e, "198.51.100.7")
	if code, _ := approveLogin(t, e, l, admin); code != 200 {
		t.Fatal("approve")
	}
	for _, nonce := range []string{"", l.Secret, strings.Repeat("A", 43)} {
		if code, _, tok := pollLogin(t, e, l, nonce, "203.0.113.9"); code != 404 || tok != "" {
			t.Fatalf("nonce %q: %d", nonce, code)
		}
	}
	// the waiting browser still gets it
	if _, status, tok := pollLogin(t, e, l, l.Nonce, "198.51.100.7"); status != "approved" || tok == "" {
		t.Fatalf("rightful browser: %q", status)
	}
}

func TestLoginQRExpires(t *testing.T) {
	e := newEnv(t)
	admin, _ := e.store.Enable("test")
	now := time.Now()
	e.svc.loginQR.now = func() time.Time { return now }
	l := startLogin(t, e, "198.51.100.7")
	now = now.Add(loginQRTTL + time.Second)
	if code, _ := approveLogin(t, e, l, admin); code != 404 {
		t.Fatalf("approve after expiry: %d", code)
	}
	if code, _, _ := pollLogin(t, e, l, l.Nonce, "198.51.100.7"); code != 404 {
		t.Fatalf("poll after expiry: %d", code)
	}

	// approved in time but collected too late: nothing is minted
	now = time.Now()
	l = startLogin(t, e, "198.51.100.7")
	if code, _ := approveLogin(t, e, l, admin); code != 200 {
		t.Fatal("approve")
	}
	now = now.Add(loginQRTTL + time.Second)
	if code, _, tok := pollLogin(t, e, l, l.Nonce, "198.51.100.7"); code != 404 || tok != "" {
		t.Fatalf("late poll: %d", code)
	}
	if st, _ := e.store.Load(); len(st.Devices) != 0 {
		t.Fatalf("a device was minted for an expired request: %d", len(st.Devices))
	}
}

func TestLoginQRApproveNeedsATokenAndTheSecret(t *testing.T) {
	e := newEnv(t)
	admin, _ := e.store.Enable("test")
	l := startLogin(t, e, "198.51.100.7")
	if code, _ := approveLogin(t, e, l, ""); code != 401 {
		t.Fatalf("no token: %d", code)
	}
	if code, _ := approveLogin(t, e, l, "kaw_"+strings.Repeat("x", 43)); code != 401 {
		t.Fatalf("bad token: %d", code)
	}
	wrong := l
	wrong.Secret = strings.Repeat("B", 43)
	if code, _ := approveLogin(t, e, wrong, admin); code != 404 {
		t.Fatalf("wrong secret: %d", code)
	}
	// a wrong secret ends the request: it cannot be guessed at
	if code, _ := approveLogin(t, e, l, admin); code != 404 {
		t.Fatalf("after a wrong secret: %d", code)
	}
}

// A user's approval gives a user-level token for that user's room, never more.
func TestLoginQRDeviceHasTheApproversRole(t *testing.T) {
	e := newEnv(t)
	_, _ = e.store.Enable("test")
	_, user, _ := e.store.AddUser("nok", "")
	l := startLogin(t, e, "198.51.100.7")
	if code, body := approveLogin(t, e, l, user); code != 200 {
		t.Fatalf("user approve: %d %s", code, body)
	}
	_, status, tok := pollLogin(t, e, l, l.Nonce, "198.51.100.7")
	if status != "approved" {
		t.Fatal(status)
	}
	id, ok := e.store.Identify(tok)
	if !ok || id.Role != RoleUser || id.Name != "nok" {
		t.Fatalf("browser = %+v", id)
	}
	if code, _, _ := e.do(t, "GET", "/aiagent/api/tokens", tok, ""); code != 403 {
		t.Fatalf("user browser reached an admin route: %d", code)
	}
}

// A browser signed in by a phone ends when that phone is revoked: a stolen
// phone cannot leave a key behind that outlives it.
func TestLoginQRBrowserEndsWithTheApprovingPhone(t *testing.T) {
	e := newEnv(t)
	admin, _ := e.store.Enable("test")
	phone := pairDevice(t, e, admin)
	l := startLogin(t, e, "198.51.100.7")
	if code, body := approveLogin(t, e, l, phone); code != 200 {
		t.Fatalf("phone approve: %d %s", code, body)
	}
	_, _, browser := pollLogin(t, e, l, l.Nonce, "198.51.100.7")
	if _, ok := e.store.Identify(browser); !ok {
		t.Fatal("browser token does not work")
	}
	pid, _ := e.store.Identify(phone)
	if err := e.store.RevokeDevice(Identity{Role: RoleAdmin}, pid.DeviceID); err != nil {
		t.Fatal(err)
	}
	if _, ok := e.store.Identify(browser); ok {
		t.Fatal("the browser outlived the phone that signed it in")
	}
}

func TestLoginQRCreationIsRateLimited(t *testing.T) {
	e := newEnv(t)
	_, _ = e.store.Enable("test")
	now := time.Now()
	e.svc.loginQR.now = func() time.Time { return now }
	for i := 0; i < loginQRPerAddr; i++ {
		startLogin(t, e, "198.51.100.8")
	}
	if code, _ := loginCall(t, e, "POST", "/aiagent/login-qr", "", "", `{"origin":"https://aiagent.joinanny.com"}`, "198.51.100.8"); code != 429 {
		t.Fatalf("over the limit: %d", code)
	}
	// a minute later the address may ask again
	now = now.Add(loginQRWindow)
	if code, _ := loginCall(t, e, "POST", "/aiagent/login-qr", "", "", `{"origin":"https://aiagent.joinanny.com"}`, "198.51.100.8"); code != 200 {
		t.Fatalf("after the window: %d", code)
	}
	// and a wrong origin is refused outright
	if code, _ := loginCall(t, e, "POST", "/aiagent/login-qr", "", "", `{"origin":"http://evil.example"}`, "198.51.100.10"); code != 400 {
		t.Fatalf("http origin: %d", code)
	}
}

func TestLoginQRPollGuessingIsLimited(t *testing.T) {
	e := newEnv(t)
	_, _ = e.store.Enable("test")
	l := startLogin(t, e, "198.51.100.7")
	for i := 0; i < loginQRMaxFailures; i++ {
		pollLogin(t, e, l, strings.Repeat("C", 43), "203.0.113.5")
	}
	if code, _, _ := pollLogin(t, e, l, l.Nonce, "203.0.113.5"); code != 429 {
		t.Fatalf("after many wrong nonces: %d", code)
	}
}

// The login QR is hidden like every other route while the service is off.
func TestLoginQRHiddenWhileDisabled(t *testing.T) {
	e := newEnv(t)
	if code, _ := loginCall(t, e, "POST", "/aiagent/login-qr", "", "", `{"origin":"https://aiagent.joinanny.com"}`, ""); code != 404 {
		t.Fatalf("disabled: %d", code)
	}
}

// Only the listed routes answer without a token; everything under
// /aiagent/api refuses a request that has none.
func TestOnlyTheListedRoutesAnswerWithoutAToken(t *testing.T) {
	e := newEnv(t)
	_, _ = e.store.Enable("test")
	public := map[string]bool{
		"GET /": true, "GET /aiagent": true, "GET /aiagent/": true,
		"GET /aiagent/i18n.js": true, "GET /aiagent/app.js": true, "GET /aiagent/styles.css": true,
		"GET /aiagent/manifest.webmanifest": true, "GET /aiagent/assets/:name": true, "GET /aiagent/fonts/:name": true,
		"GET /aiagent/review-view/:ticket": true,
		"POST /aiagent/pair":               true,
		// the browser's QR login: create, and poll with its nonce
		"POST /aiagent/login-qr":    true,
		"GET /aiagent/login-qr/:id": true,
		// a session's own `aiagent channel`: a channel key or per-session
		// secret instead of a token, loopback only, 404 while serve
		// --channels is off (channels_http_test.go pins all three)
		"POST /aiagent/channel/register":   true,
		"POST /aiagent/channel/poll":       true,
		"POST /aiagent/channel/permission": true,
		// the test env's own SPA fallback and the 404 catch-all
		"GET /*": true,
	}
	checked := 0
	for _, r := range e.app.GetRoutes(true) {
		switch r.Method {
		case "GET", "POST", "PUT", "DELETE", "PATCH":
		default:
			continue
		}
		key := r.Method + " " + r.Path
		if strings.HasPrefix(r.Path, "/aiagent/api/") {
			path := strings.NewReplacer(":id", "abc123", ":name", "x", ":provider", "claude", ":ticket", "t").Replace(r.Path)
			e.svc.failures = map[string][]time.Time{}
			code, _ := loginCall(t, e, r.Method, path, "", "", "", "")
			if code != 401 {
				t.Errorf("%s answered %d without a token", key, code)
			}
			checked++
			continue
		}
		if r.Path == "/aiagent/*" {
			continue // hidden: always 404
		}
		if !public[key] {
			t.Errorf("unexpected route without a token: %s", key)
		}
	}
	// the walk itself must have happened, including the new approve route
	if checked < 50 {
		t.Fatalf("only %d api routes walked", checked)
	}
}
