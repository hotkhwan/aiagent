package world

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image/png"
	"io"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

var pairTicketRE = regexp.MustCompile(`^pair_[A-Za-z0-9_-]{43}$`)

// pairCall makes one request and returns the status, the Cache-Control
// header and the raw body.
func pairCall(t *testing.T, e *env, path, token, body, ip string) (int, string, string) {
	t.Helper()
	req := httptest.NewRequest("POST", path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set(TokenHeader, token)
	}
	if ip != "" {
		req.Header.Set("Cf-Connecting-Ip", ip)
	}
	resp, err := e.app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header.Get("Cache-Control"), string(b)
}

type pairIssued struct {
	Code      string `json:"code"`
	ExpiresAt string `json:"expiresAt"`
	QR        string `json:"qr"`
}

// issuePair asks for a pairing code with token and returns it and its ticket.
func issuePair(t *testing.T, e *env, token string) (pairIssued, string) {
	t.Helper()
	code, cc, body := pairCall(t, e, "/aiagent/api/connect/pair", token, `{"origin":"https://aiagent.joinanny.com"}`, "")
	if code != 200 || !strings.Contains(cc, "no-store") {
		t.Fatalf("issue: %d %q %s", code, cc, body)
	}
	var env struct {
		Code    string     `json:"code"`
		Details pairIssued `json:"details"`
	}
	if err := json.Unmarshal([]byte(body), &env); err != nil || env.Code != "SUCCESS" {
		t.Fatalf("issue body: %s", body)
	}
	u, err := url.Parse(env.Details.Code)
	if err != nil || u.Scheme != "aiagent" || u.Host != "pair" {
		t.Fatalf("code = %q", env.Details.Code)
	}
	ticket := u.Query().Get("c")
	if !pairTicketRE.MatchString(ticket) {
		t.Fatalf("ticket shape %q", ticket)
	}
	if u.Query().Get("u") != "https://aiagent.joinanny.com" {
		t.Fatalf("origin in code: %q", env.Details.Code)
	}
	if !strings.HasPrefix(env.Details.Code, "aiagent://pair?c=pair_") {
		t.Fatalf("c comes first: %q", env.Details.Code)
	}
	return env.Details, ticket
}

func redeem(t *testing.T, e *env, ticket, ip string) (int, string, string) {
	t.Helper()
	return pairCall(t, e, "/aiagent/pair", "", `{"code":"`+ticket+`"}`, ip)
}

func redeemedToken(t *testing.T, body string) string {
	t.Helper()
	var env struct {
		Code    string `json:"code"`
		Details struct {
			Token string `json:"token"`
		} `json:"details"`
	}
	if err := json.Unmarshal([]byte(body), &env); err != nil || env.Code != "SUCCESS" {
		t.Fatalf("redeem body: %s", body)
	}
	return env.Details.Token
}

func TestPairIssueNeedsATokenAndDrawsTheQR(t *testing.T) {
	e := newEnv(t)
	_, _ = e.store.Enable("test")
	_, user, _ := e.store.AddUser("nok", "")

	if code, _, _ := pairCall(t, e, "/aiagent/api/connect/pair", "", `{"origin":"https://aiagent.joinanny.com"}`, ""); code != 401 {
		t.Fatalf("no token: %d", code)
	}
	if code, _, _ := pairCall(t, e, "/aiagent/api/connect/pair", user, `{"origin":"http://aiagent.joinanny.com"}`, ""); code != 400 {
		t.Fatalf("http origin: %d", code)
	}
	d, _ := issuePair(t, e, user)
	exp, err := time.Parse(time.RFC3339, d.ExpiresAt)
	if err != nil {
		t.Fatalf("expiresAt %q: %v", d.ExpiresAt, err)
	}
	if left := time.Until(exp); left < 9*time.Minute || left > 10*time.Minute+time.Second {
		t.Fatalf("expires in %v", left)
	}
	b64, ok := strings.CutPrefix(d.QR, "data:image/png;base64,")
	if !ok {
		t.Fatalf("qr = %.40q", d.QR)
	}
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := png.Decode(bytes.NewReader(raw)); err != nil {
		t.Fatalf("not a png: %v", err)
	}
}

func TestPairRedeemGivesTheIssuersTokenOnce(t *testing.T) {
	e := newEnv(t)
	admin, _ := e.store.Enable("test")
	_, user, _ := e.store.AddUser("nok", "")

	for _, tok := range []string{admin, user} {
		_, ticket := issuePair(t, e, tok)
		code, cc, body := redeem(t, e, ticket, "203.0.113.1")
		if code != 200 || !strings.Contains(cc, "no-store") {
			t.Fatalf("redeem: %d %q %s", code, cc, body)
		}
		if got := redeemedToken(t, body); got != tok {
			t.Fatalf("redeemed a different token")
		}
		if code, _, _ := redeem(t, e, ticket, "203.0.113.1"); code != 404 {
			t.Fatalf("second redeem: %d", code)
		}
	}
}

func TestPairRedeemIsHiddenWhileDisabled(t *testing.T) {
	e := newEnv(t)
	admin, _ := e.store.Enable("test")
	_, ticket := issuePair(t, e, admin)
	_ = e.store.Disable("test")
	code, _, body := redeem(t, e, ticket, "")
	if code != 404 || !strings.Contains(body, "NOT_FOUND") {
		t.Fatalf("disabled: %d %s", code, body)
	}
}

func TestPairConcurrentRedeemsHaveOneWinner(t *testing.T) {
	e := newEnv(t)
	admin, _ := e.store.Enable("test")
	_, ticket := issuePair(t, e, admin)
	var wg sync.WaitGroup
	var mu sync.Mutex
	wins := 0
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			req := httptest.NewRequest("POST", "/aiagent/pair", strings.NewReader(`{"code":"`+ticket+`"}`))
			req.Header.Set("Content-Type", "application/json")
			resp, err := e.app.Test(req)
			if err == nil && resp.StatusCode == 200 {
				mu.Lock()
				wins++
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()
	if wins != 1 {
		t.Fatalf("%d redeems won", wins)
	}
}

func TestPairFailuresLookTheSame(t *testing.T) {
	e := newEnv(t)
	admin, _ := e.store.Enable("test")
	uid, user, _ := e.store.AddUser("nok", "")
	now := time.Now()
	e.svc.pair.now = func() time.Time { return now }
	const ip = "198.51.100.2"

	answers := map[string]string{}
	fail := func(name, body string) {
		t.Helper()
		code, cc, got := pairCall(t, e, "/aiagent/pair", "", body, ip)
		if code != 404 || !strings.Contains(cc, "no-store") {
			t.Errorf("%s: %d %q %s", name, code, cc, got)
		}
		answers[name] = got
	}
	ticketBody := func(ticket string) string { return `{"code":"` + ticket + `"}` }

	fail("bad shape", ticketBody("pair_short"))
	fail("no prefix", ticketBody(strings.Repeat("A", 48)))
	fail("unknown", ticketBody("pair_"+strings.Repeat("A", 43)))
	fail("empty", ticketBody(""))
	fail("not json", `nope`)

	_, used := issuePair(t, e, admin)
	if code, _, _ := redeem(t, e, used, "198.51.100.1"); code != 200 {
		t.Fatal("first redeem failed")
	}
	fail("used", ticketBody(used))

	// revoked: the user's token is withdrawn while its ticket waits
	_, revoked := issuePair(t, e, user)
	if err := e.store.RevokeUser(uid.ID); err != nil {
		t.Fatal(err)
	}
	fail("revoked", ticketBody(revoked))

	_, old := issuePair(t, e, admin)
	now = now.Add(pairTTL + time.Second)
	fail("expired", ticketBody(old))

	var want string
	for name, got := range answers {
		if want == "" {
			want = got
		} else if got != want {
			t.Errorf("%s answers %s; another answers %s", name, got, want)
		}
	}
	// one issued after the clock moved still works
	_, fresh := issuePair(t, e, admin)
	if code, _, _ := redeem(t, e, fresh, "198.51.100.10"); code != 200 {
		t.Errorf("fresh ticket: %d", code)
	}
}

func TestPairFailedRedeemsAreRateLimited(t *testing.T) {
	e := newEnv(t)
	admin, _ := e.store.Enable("test")
	now := time.Now()
	e.svc.pair.now = func() time.Time { return now }
	_, good := issuePair(t, e, admin)
	bad := "pair_" + strings.Repeat("B", 43)
	for i := 0; i < pairMaxFailures; i++ {
		if code, _, _ := redeem(t, e, bad, "192.0.2.7"); code != 404 {
			t.Fatalf("try %d: %d", i, code)
		}
	}
	if code, _, _ := redeem(t, e, good, "192.0.2.7"); code != 429 {
		t.Fatalf("after %d failures: %d", pairMaxFailures, code)
	}
	// another address is not held back
	// (the test peer is not loopback, so Cf-Connecting-Ip is not trusted and
	// every request here is one address; ask the table directly)
	if e.svc.pair.limited("192.0.2.8", now) {
		t.Fatal("another address is held back")
	}
	// and the limit lifts after the window
	now = now.Add(pairFailureWindow + time.Second)
	if code, _, _ := redeem(t, e, good, "192.0.2.7"); code != 200 {
		t.Fatalf("after the window: %d", code)
	}
}

func TestPairTableIsCapped(t *testing.T) {
	e := newEnv(t)
	admin, _ := e.store.Enable("test")
	_, first := issuePair(t, e, admin)
	var last string
	for i := 0; i < pairMaxTickets; i++ {
		_, last = issuePair(t, e, admin)
	}
	e.svc.pair.mu.Lock()
	n := len(e.svc.pair.tickets)
	e.svc.pair.mu.Unlock()
	if n > pairMaxTickets {
		t.Fatalf("%d tickets held", n)
	}
	if code, _, _ := redeem(t, e, first, "192.0.2.20"); code != 404 {
		t.Fatalf("oldest ticket kept: %d", code)
	}
	if code, _, _ := redeem(t, e, last, "192.0.2.20"); code != 200 {
		t.Fatalf("newest ticket lost: %d", code)
	}
}

func TestPairNeverWritesTheTicketOrToken(t *testing.T) {
	e := newEnv(t)
	admin, _ := e.store.Enable("test")
	_, user, _ := e.store.AddUser("nok", "")
	_, t1 := issuePair(t, e, user)
	_, t2 := issuePair(t, e, admin)
	redeem(t, e, t1, "")
	redeem(t, e, t1, "")
	b, _ := os.ReadFile(e.auditFP)
	for _, s := range []string{user, admin, t1, t2, strings.TrimPrefix(t1, "pair_")} {
		if bytes.Contains(b, []byte(s)) {
			t.Fatal("a ticket or token was written to the audit log")
		}
	}
	// nor anywhere under the service's directories
	walkContains(t, e.dir, []string{t1, t2, strings.TrimPrefix(t2, "pair_")})
}

// walkContains fails the test if any file under dir holds one of the strings.
func walkContains(t *testing.T, dir string, needles []string) {
	t.Helper()
	entries, _ := os.ReadDir(dir)
	for _, en := range entries {
		p := dir + "/" + en.Name()
		if en.IsDir() {
			walkContains(t, p, needles)
			continue
		}
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		for _, n := range needles {
			if bytes.Contains(b, []byte(n)) {
				t.Errorf("%s holds a ticket", p)
			}
		}
	}
}

// With a room per user, a user's request is sent on to their room unless the
// path is answered here: the tickets live in this service, so issuing must be.
func TestPairIssueIsAnsweredHere(t *testing.T) {
	if !localPath("/connect/pair") {
		t.Error("/connect/pair must be answered by the main service")
	}
	allowed := false
	for _, re := range userRoutes["POST"] {
		allowed = allowed || re.MatchString("/connect/pair")
	}
	if !allowed {
		t.Error("a user token may ask for a pairing code")
	}
}
