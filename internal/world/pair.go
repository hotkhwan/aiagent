// internal/world/pair.go
//
// Pairing codes: how the phone app gets a token without the token ever being
// drawn on the screen.
//
//	POST /aiagent/api/connect/pair  token — {origin} → {code, expiresAt, qr}
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
		if !now.Before(v.expires) {
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
	o, err := ParsePushRelay(origin) // the same rule as connectCode
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
	}
	if json.Unmarshal(c.Body(), &body) != nil || !pairCodeRE.MatchString(body.Code) {
		s.pair.failed(ip, now)
		return pairInvalid(c)
	}
	token, ok := s.pair.take(body.Code, now)
	if ok {
		// a token revoked or reissued since the code was made opens nothing
		_, ok = s.Store.IdentifyHash(sha256.Sum256([]byte(token)))
	}
	if !ok {
		s.pair.failed(ip, now)
		return pairInvalid(c)
	}
	return c.JSON(okEnv(fiber.Map{"token": token}))
}
