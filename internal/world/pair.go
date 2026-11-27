// internal/world/pair.go
//
// Pairing codes: how the phone app gets a token without the token ever being
// drawn on the screen.
//
//	POST /aiagent/api/connect/pair  token — {origin} → {code, expiresAt, qr}
//	(redeeming a code gives the phone its own token with the page's scope)
//	POST /aiagent/pair              no token — {code: "pair_…"} → {token}
//
// The page asks for a code; the QR holds only a one-time ticket and this
// server's address:
//
//	aiagent://pair?c=pair_<43 base64url chars>&u=<https origin>
//
// The app sends the ticket back to /aiagent/pair and gets the token the page
// was using. A ticket works once and for ten minutes, so a photo of the QR is
// worth nothing once the phone has used it or the time is up.
//
// The server keeps only token hashes, so the token to hand over is the one
// the page presented, held in memory beside sha256(ticket) and nowhere else:
// never on disk, never in the audit log (issuing changes nothing that lasts,
// the same reasoning as connect.go). The table is small and capped; a restart
// empties it and the page simply asks again.
package world

import (
	"crypto/rand"
	"crypto/sha256"
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

const (
	pairTTL           = 10 * time.Minute
	pairMaxTickets    = 50
	pairMaxFailures   = 10
	pairFailureWindow = time.Minute
	pairPrefix        = "pair_"
)

var pairCodeRE = regexp.MustCompile(`^pair_[A-Za-z0-9_-]{43}$`)

type pairTicket struct {
	token   string
	expires time.Time
}

// pairState is the live tickets and the failed redeems per address.
type pairState struct {
	mu      sync.Mutex
	tickets map[[32]byte]pairTicket
	fails   map[string][]time.Time
	now     func() time.Time // nil is time.Now; tests move it
}

func (p *pairState) clock() time.Time {
	if p.now != nil {
		return p.now()
	}
	return time.Now()
}

// issue stores a new ticket for token and returns it.
func (p *pairState) issue(token string, now time.Time) (string, time.Time, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", time.Time{}, err
	}
	ticket := pairPrefix + base64.RawURLEncoding.EncodeToString(raw[:])
	expires := now.Add(pairTTL)
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.tickets == nil {
		p.tickets = map[[32]byte]pairTicket{}
	}
	for k, v := range p.tickets {
		// only the newest code for a token works: an old screen opens nothing
		if !now.Before(v.expires) || v.token == token {
			delete(p.tickets, k)
		}
	}
	for len(p.tickets) >= pairMaxTickets {
		// every ticket lives as long, so the one that ends first is the oldest
		var oldest [32]byte
		var at time.Time
		for k, v := range p.tickets {
			if at.IsZero() || v.expires.Before(at) {
				oldest, at = k, v.expires
			}
		}
		delete(p.tickets, oldest)
	}
	p.tickets[sha256.Sum256([]byte(ticket))] = pairTicket{token: token, expires: expires}
	return ticket, expires, nil
}

// cancel ends every code issued for token.
func (p *pairState) cancel(token string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for k, v := range p.tickets {
		if v.token == token {
			delete(p.tickets, k)
		}
	}
}

// take removes the ticket and returns its token if it was still live. The
// lookup and the delete are one step, so two redeems never both win.
func (p *pairState) take(ticket string, now time.Time) (string, bool) {
	sum := sha256.Sum256([]byte(ticket))
	p.mu.Lock()
	defer p.mu.Unlock()
	v, ok := p.tickets[sum]
	if !ok {
		return "", false
	}
	delete(p.tickets, sum)
	if !now.Before(v.expires) {
		return "", false
	}
	return v.token, true
}

func (p *pairState) limited(ip string, now time.Time) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	kept := p.fails[ip][:0]
	for _, t := range p.fails[ip] {
		if now.Sub(t) < pairFailureWindow {
			kept = append(kept, t)
		}
	}
	if len(kept) == 0 {
		delete(p.fails, ip)
	} else {
		p.fails[ip] = kept
	}
	return len(kept) >= pairMaxFailures
}

func (p *pairState) failed(ip string, now time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.fails == nil || len(p.fails) > 4096 {
		p.fails = map[string][]time.Time{}
	}
	p.fails[ip] = append(p.fails[ip], now)
}

func pairCode(origin, ticket string) (string, error) {
	o, err := ParsePushRelay(origin) // an https origin (loopback http)
	if err != nil || o == "" {
		return "", ErrBadRequest("origin must be the https address of this server")
	}
	q := url.Values{}
	q.Set("c", ticket)
	q.Set("u", o)
	return "aiagent://pair?" + q.Encode(), nil // keys sorted: c, then u
}

func (s *Service) handleConnectPair(c fiber.Ctx) error {
	c.Set("Cache-Control", "no-store")
	var body struct {
		Origin string `json:"origin"`
	}
	if err := json.Unmarshal(c.Body(), &body); err != nil {
		return s.fail(c, ErrBadRequest(`expected {"origin": "https://…"}`))
	}
	if _, err := pairCode(body.Origin, pairPrefix); err != nil {
		return s.fail(c, err)
	}
	// a phone pairs no further phones: a stolen one could otherwise mint
	// tokens that outlive its own revoke (devices.go)
	if identity(c).DeviceID != "" {
		return s.fail(c, &APIError{403, "AIAGENT_FORBIDDEN", "จับคู่อุปกรณ์ใหม่ได้จากหน้าเว็บเท่านั้น"})
	}
	// the header points into a reused buffer; the ticket outlives the request
	token := strings.Clone(c.Get(TokenHeader))
	ticket, expires, err := s.pair.issue(token, s.pair.clock())
	if err != nil {
		return s.fail(c, err)
	}
	code, err := pairCode(body.Origin, ticket)
	if err != nil {
		return s.fail(c, err)
	}
	img, err := qr.Encode(code, qr.M)
	if err != nil {
		return s.fail(c, err)
	}
	img.Scale = 6
	return c.JSON(okEnv(fiber.Map{
		"code":      code,
		"expiresAt": expires.UTC().Format(time.RFC3339),
		"qr":        "data:image/png;base64," + base64.StdEncoding.EncodeToString(img.PNG()),
	}))
}

// handleConnectPairCancel ends the caller's code when the dialog closes, so a
// code left on a screen or in a photo does not stay a key for ten minutes.
func (s *Service) handleConnectPairCancel(c fiber.Ctx) error {
	c.Set("Cache-Control", "no-store")
	s.pair.cancel(c.Get(TokenHeader))
	return c.JSON(okEnv(fiber.Map{}))
}

// pairInvalid is the one answer to every redeem that fails, so a caller
// learns nothing about why.
func pairInvalid(c fiber.Ctx) error {
	return c.Status(fiber.StatusNotFound).JSON(failEnv("AIAGENT_PAIR_INVALID", "รหัสจับคู่นี้ใช้ไม่ได้ ขอรหัสใหม่จากหน้าเว็บ"))
}

func (s *Service) handlePairRedeem(c fiber.Ctx) error {
	if !s.Store.Enabled() {
		return hidden(c)
	}
	c.Set("Cache-Control", "no-store")
	c.Set("X-Content-Type-Options", "nosniff")
	now := s.pair.clock()
	ip := clientAddr(c)
	if s.pair.limited(ip, now) {
		return c.Status(fiber.StatusTooManyRequests).JSON(failEnv("AIAGENT_LOCKED", "ลองรหัสผิดหลายครั้งเกินไป รอสักครู่แล้วลองใหม่"))
	}
	var body struct {
		Code string `json:"code"`
		// Name labels the phone in the device list; optional.
		Name string `json:"name"`
	}
	if json.Unmarshal(c.Body(), &body) != nil || !pairCodeRE.MatchString(body.Code) {
		s.pair.failed(ip, now)
		return pairInvalid(c)
	}
	token, ok := s.pair.take(body.Code, now)
	var page Identity
	if ok {
		// a token revoked or reissued since the code was made opens nothing
		page, ok = s.Store.IdentifyHash(sha256.Sum256([]byte(token)))
	}
	if !ok {
		s.pair.failed(ip, now)
		_ = s.auditPair(ip, "denied")
		return pairInvalid(c)
	}
	// on record before the token leaves: no audit line, no token
	if err := s.auditPair(ip, "ok"); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(failEnv("AIAGENT_AUDIT_FAILED", "audit log unavailable — action not performed"))
	}
	// the phone gets a token of its own with the page's scope, never the
	// page's token: a lost phone is then one revoke (devices.go)
	_, phone, err := s.Store.AddDevice(page, body.Name)
	if err != nil {
		return s.fail(c, err)
	}
	return c.JSON(okEnv(fiber.Map{"token": phone}))
}

// auditPair records a redeem by where it came from, never the code or token.
func (s *Service) auditPair(ip, result string) error {
	if s.Auditor == nil {
		return nil
	}
	return s.Auditor.Write(audit.Record{Event: "result", RPC: "aiagent.pairRedeem", AdminSub: "aiagent-pair@" + ip, Result: result})
}

// handleTokenPair issues a pairing code for a user token the admin holds:
// the one it was just shown by create or reissue. The admin proves it holds
// the token by sending it back; the phone that scans the code then gets a
// device token in that user's scope, never the user token itself. Admin only
// (not in userRoutes), audited, and the token is never echoed or logged.
func (s *Service) handleTokenPair(c fiber.Ctx) error {
	c.Set("Cache-Control", "no-store")
	id := strings.Clone(c.Params("id"))
	var body struct {
		Token  string `json:"token"`
		Origin string `json:"origin"`
	}
	if err := json.Unmarshal(c.Body(), &body); err != nil {
		return s.fail(c, ErrBadRequest(`expected {"token": "kaw_…", "origin": "https://…"}`))
	}
	if _, err := pairCode(body.Origin, pairPrefix); err != nil {
		return s.fail(c, err)
	}
	who, ok := s.Store.IdentifyHash(sha256.Sum256([]byte(body.Token)))
	if !ok || who.Role != RoleUser || who.DeviceID != "" || who.UserID != id {
		return s.fail(c, ErrNotFound("ไม่พบ token นี้"))
	}
	if s.Auditor != nil {
		if err := s.Auditor.Write(audit.Record{Event: "result", RPC: "aiagent.tokenPair", AdminSub: subject(c), ParamsHash: paramsHash(id), Result: "ok"}); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(failEnv("AIAGENT_AUDIT_FAILED", "เขียนบันทึก audit ไม่ได้ จึงไม่ได้ทำคำสั่งนี้"))
		}
	}
	ticket, expires, err := s.pair.issue(strings.Clone(body.Token), s.pair.clock())
	if err != nil {
		return s.fail(c, err)
	}
	code, err := pairCode(body.Origin, ticket)
	if err != nil {
		return s.fail(c, err)
	}
	img, err := qr.Encode(code, qr.M)
	if err != nil {
		return s.fail(c, err)
	}
	img.Scale = 6
	return c.JSON(okEnv(fiber.Map{
		"code":      code,
		"expiresAt": expires.UTC().Format(time.RFC3339),
		"qr":        "data:image/png;base64," + base64.StdEncoding.EncodeToString(img.PNG()),
	}))
}
