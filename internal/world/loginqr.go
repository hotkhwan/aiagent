// internal/world/loginqr.go
//
// Signing a browser in by scanning a QR with the phone app.
//
//	POST /aiagent/login-qr                  no token — {origin} → {id, nonce, code, qr, expiresAt}
//	GET  /aiagent/login-qr/:id              no token, X-Aiagent-Login-Nonce — {status} or, once, {status, token}
//	POST /aiagent/api/login-qr/:id/info     token — {secret} → who is asking (browser, address, origin)
//	POST /aiagent/api/login-qr/:id/approve  token — {secret} → the browser may collect its token
//
// The login page asks for a request and draws its QR:
//
//	aiagent://login?i=<id>&s=<secret>&u=<https origin>
//
// It keeps the nonce in memory and polls with it. The app scans the QR, shows
// who is asking, asks Face ID and approves with its own token. The next poll
// that presents the nonce receives a NEW device token (devices.go) with the
// approver's scope, never more: an admin's approval gives an admin device, a
// user's a device of that user's room. A phone's approval ties the browser to
// that phone, so revoking the phone ends the browser too.
//
// Three secrets, each held by one party: the id names the request (in the QR
// and the poll URL), the secret proves the approver saw the QR, and the nonce
// proves the poller is the page that asked. A photo of the QR has the id and
// secret but not the nonce, so it can approve (with a token) but never
// collect. Only their SHA-256 is kept, in memory; a restart empties the table
// and the page asks again. Nothing is written to disk until the poll that
// collects, which mints the device and audits it first.
package world

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/hotkhwan/aiagent/internal/audit"
	"rsc.io/qr"
)

// LoginNonceHeader carries the polling page's nonce.
const LoginNonceHeader = "X-Aiagent-Login-Nonce"

const (
	loginQRTTL         = 2 * time.Minute
	loginQRMax         = 100
	loginQRPerAddr     = 5 // requests one address may make per window
	loginQRMaxFailures = 10
	loginQRWindow      = time.Minute
	loginQRDeviceName  = "Web QR"
)

var (
	loginIDRE     = regexp.MustCompile(`^[A-Za-z0-9_-]{22}$`)
	loginSecretRE = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)
)

type loginReq struct {
	secret, nonce [32]byte
	origin, agent string
	addr          string
	expires       time.Time
	approved      bool
	// approver is the SHA-256 of the approving token: the identity is
	// looked up again at collection, so a token revoked in between opens
	// nothing.
	approver [32]byte
}

type loginQRState struct {
	mu    sync.Mutex
	reqs  map[string]*loginReq
	made  map[string][]time.Time
	fails map[string][]time.Time
	now   func() time.Time // nil is time.Now; tests move it
}

func (l *loginQRState) clock() time.Time {
	if l.now != nil {
		return l.now()
	}
	return time.Now()
}

// inWindow drops the times older than the window and returns how many remain.
func inWindow(m map[string][]time.Time, key string, now time.Time) int {
	kept := m[key][:0]
	for _, t := range m[key] {
		if now.Sub(t) < loginQRWindow {
			kept = append(kept, t)
		}
	}
	if len(kept) == 0 {
		delete(m, key)
	} else {
		m[key] = kept
	}
	return len(kept)
}

func (l *loginQRState) limited(addr string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.fails != nil && inWindow(l.fails, addr, now) >= loginQRMaxFailures
}

func (l *loginQRState) failed(addr string, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.fails == nil || len(l.fails) > 4096 {
		l.fails = map[string][]time.Time{}
	}
	l.fails[addr] = append(l.fails[addr], now)
}

func randomB64(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

var errLoginBusy = &APIError{429, "AIAGENT_SLOW_DOWN", "ขอ QR เข้าสู่ระบบถี่เกินไป รอสักครู่แล้วลองใหม่"}

// open stores a new request and returns its id, secret and nonce.
func (l *loginQRState) open(origin, agent, addr string, now time.Time) (id, secret, nonce string, expires time.Time, err error) {
	if id, err = randomB64(16); err != nil {
		return
	}
	if secret, err = randomB64(32); err != nil {
		return
	}
	if nonce, err = randomB64(32); err != nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.reqs == nil {
		l.reqs = map[string]*loginReq{}
	}
	if l.made == nil || len(l.made) > 4096 {
		l.made = map[string][]time.Time{}
	}
	if inWindow(l.made, addr, now) >= loginQRPerAddr {
		err = errLoginBusy
		return
	}
	for k, r := range l.reqs {
		if !now.Before(r.expires) {
			delete(l.reqs, k)
		}
	}
	// a full table refuses new requests rather than dropping a live one
	if len(l.reqs) >= loginQRMax {
		err = errLoginBusy
		return
	}
	l.made[addr] = append(l.made[addr], now)
	expires = now.Add(loginQRTTL)
	l.reqs[id] = &loginReq{
		secret: sha256.Sum256([]byte(secret)), nonce: sha256.Sum256([]byte(nonce)),
		origin: origin, agent: agent, addr: addr, expires: expires,
	}
	return
}

func sumEqual(a, b [32]byte) bool { return subtle.ConstantTimeCompare(a[:], b[:]) == 1 }

var errLoginInvalid = ErrNotFound("QR เข้าสู่ระบบนี้ใช้ไม่ได้แล้ว ให้หน้าเว็บสร้าง QR ใหม่")

// live returns a copy of the request the approver may still act on: known,
// not expired, not yet approved, and with the right secret. A wrong secret
// ends the request, so it cannot be guessed at.
func (l *loginQRState) live(id, secret string, now time.Time) (loginReq, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	r, ok := l.reqs[id]
	if !ok {
		return loginReq{}, errLoginInvalid
	}
	if !sumEqual(r.secret, sha256.Sum256([]byte(secret))) || !now.Before(r.expires) {
		delete(l.reqs, id)
		return loginReq{}, errLoginInvalid
	}
	if r.approved {
		return loginReq{}, errLoginInvalid
	}
	return *r, nil
}

// approve marks the request approved by the holder of approver, once.
func (l *loginQRState) approve(id, secret string, approver [32]byte, now time.Time) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	r, ok := l.reqs[id]
	if !ok {
		return errLoginInvalid
	}
	if !sumEqual(r.secret, sha256.Sum256([]byte(secret))) || !now.Before(r.expires) {
		delete(l.reqs, id)
		return errLoginInvalid
	}
	if r.approved {
		return errLoginInvalid
	}
	r.approved, r.approver = true, approver
	return nil
}

type pollResult int

const (
	pollInvalid pollResult = iota
	pollPending
	pollApproved
)

// collect answers one poll. An approved request is removed in the same step,
// so two polls never both collect.
func (l *loginQRState) collect(id, nonce string, now time.Time) (pollResult, [32]byte) {
	l.mu.Lock()
	defer l.mu.Unlock()
	r, ok := l.reqs[id]
	if !ok {
		return pollInvalid, [32]byte{}
	}
	if !sumEqual(r.nonce, sha256.Sum256([]byte(nonce))) {
		return pollInvalid, [32]byte{}
	}
	if !now.Before(r.expires) {
		delete(l.reqs, id)
		return pollInvalid, [32]byte{}
	}
	if !r.approved {
		return pollPending, [32]byte{}
	}
	delete(l.reqs, id)
	return pollApproved, r.approver
}

// userAgent is a browser's own description, cut to printable ASCII so it can
// be shown in the app as plain text.
func userAgent(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= 0x20 && r < 0x7f {
			b.WriteRune(r)
		}
		if b.Len() >= 160 {
			break
		}
	}
	return b.String()
}

func loginCode(origin, id, secret string) (string, error) {
	o, err := ParsePushRelay(origin) // the same rule as pairCode
	if err != nil || o == "" {
		return "", ErrBadRequest("origin must be the https address of this server")
	}
	q := url.Values{}
	q.Set("i", id)
	q.Set("s", secret)
	q.Set("u", o)
	return "aiagent://login?" + q.Encode(), nil
}

func loginInvalidReply(c fiber.Ctx) error {
	return c.Status(fiber.StatusNotFound).JSON(failEnv("AIAGENT_LOGIN_QR_INVALID", "QR เข้าสู่ระบบนี้ใช้ไม่ได้แล้ว สร้าง QR ใหม่"))
}

func (s *Service) handleLoginQRCreate(c fiber.Ctx) error {
	if !s.Store.Enabled() {
		return hidden(c)
	}
	c.Set("Cache-Control", "no-store")
	c.Set("X-Content-Type-Options", "nosniff")
	var body struct {
		Origin string `json:"origin"`
	}
	if json.Unmarshal(c.Body(), &body) != nil {
		return s.fail(c, ErrBadRequest(`expected {"origin": "https://…"}`))
	}
	origin, err := ParsePushRelay(body.Origin)
	if err != nil || origin == "" {
		return s.fail(c, ErrBadRequest("origin must be the https address of this server"))
	}
	id, secret, nonce, expires, err := s.loginQR.open(origin, userAgent(c.Get("User-Agent")), clientAddr(c), s.loginQR.clock())
	if err != nil {
		return s.fail(c, err)
	}
	code, err := loginCode(origin, id, secret)
	if err != nil {
		return s.fail(c, err)
	}
	img, err := qr.Encode(code, qr.M)
	if err != nil {
		return s.fail(c, err)
	}
	img.Scale = 6
	return c.JSON(okEnv(fiber.Map{
		"id":        id,
		"nonce":     nonce,
		"code":      code,
		"expiresAt": expires.UTC().Format(time.RFC3339),
		"qr":        "data:image/png;base64," + base64.StdEncoding.EncodeToString(img.PNG()),
	}))
}

func (s *Service) handleLoginQRPoll(c fiber.Ctx) error {
	if !s.Store.Enabled() {
		return hidden(c)
	}
	c.Set("Cache-Control", "no-store")
	c.Set("X-Content-Type-Options", "nosniff")
	now := s.loginQR.clock()
	addr := clientAddr(c)
	if s.loginQR.limited(addr, now) {
		return c.Status(fiber.StatusTooManyRequests).JSON(failEnv("AIAGENT_LOCKED", "ลองผิดหลายครั้งเกินไป รอสักครู่แล้วลองใหม่"))
	}
	id := strings.Clone(c.Params("id"))
	nonce := c.Get(LoginNonceHeader)
	if !loginIDRE.MatchString(id) || !loginSecretRE.MatchString(nonce) {
		s.loginQR.failed(addr, now)
		return loginInvalidReply(c)
	}
	res, approver := s.loginQR.collect(id, nonce, now)
	switch res {
	case pollPending:
		return c.JSON(okEnv(fiber.Map{"status": "pending"}))
	case pollInvalid:
		s.loginQR.failed(addr, now)
		return loginInvalidReply(c)
	}
	// the approver is named again now: a token revoked since opens nothing
	who, ok := s.Store.IdentifyHash(approver)
	if !ok {
		_ = s.auditLoginQR(addr, "", "denied")
		return loginInvalidReply(c)
	}
	// on record before the token leaves: no audit line, no token
	if err := s.auditLoginQR(addr, who.Name, "ok"); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(failEnv("AIAGENT_AUDIT_FAILED", "audit log unavailable — action not performed"))
	}
	_, token, err := s.Store.AddDevice(who, loginQRDeviceName)
	if err != nil {
		return s.fail(c, err)
	}
	return c.JSON(okEnv(fiber.Map{"status": "approved", "token": token}))
}

// auditLoginQR records a collection by where it came from, never a secret.
func (s *Service) auditLoginQR(addr, approver, result string) error {
	if s.Auditor == nil {
		return nil
	}
	sub := "aiagent-loginqr@" + addr
	if approver != "" {
		sub = "aiagent-loginqr:" + approver + "@" + addr
	}
	return s.Auditor.Write(audit.Record{Event: "result", RPC: "aiagent.loginQR", AdminSub: sub, Result: result})
}

func loginSecretBody(c fiber.Ctx) (string, bool) {
	var body struct {
		Secret string `json:"secret"`
	}
	if json.Unmarshal(c.Body(), &body) != nil || !loginSecretRE.MatchString(body.Secret) {
		return "", false
	}
	return body.Secret, true
}

// handleLoginQRInfo shows the approver who is asking before it decides.
func (s *Service) handleLoginQRInfo(c fiber.Ctx) error {
	c.Set("Cache-Control", "no-store")
	id := strings.Clone(c.Params("id"))
	secret, ok := loginSecretBody(c)
	if !loginIDRE.MatchString(id) || !ok {
		return s.fail(c, errLoginInvalid)
	}
	r, err := s.loginQR.live(id, secret, s.loginQR.clock())
	if err != nil {
		return s.fail(c, err)
	}
	return c.JSON(okEnv(fiber.Map{
		"origin":      r.origin,
		"browser":     r.agent,
		"address":     r.addr,
		"sameAddress": r.addr == clientAddr(c),
		"expiresAt":   r.expires.UTC().Format(time.RFC3339),
	}))
}

func (s *Service) handleLoginQRApprove(c fiber.Ctx) error {
	c.Set("Cache-Control", "no-store")
	id := strings.Clone(c.Params("id"))
	secret, ok := loginSecretBody(c)
	if !loginIDRE.MatchString(id) || !ok {
		return s.fail(c, errLoginInvalid)
	}
	approver := sha256.Sum256([]byte(c.Get(TokenHeader)))
	return s.mutate(c, "aiagent.loginQRApprove", []string{id}, func() (any, error) {
		return fiber.Map{}, s.loginQR.approve(id, secret, approver, s.loginQR.clock())
	})
}
