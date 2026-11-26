// internal/world/connect.go
//
// The connect code the phone app scans: one string with this server's address
// and a token.
//
//	aiagent://connect?u=<https origin>&t=<token>
//
// The page sends its own origin (the browser knows the public address; this
// service, behind a tunnel, does not). The QR is drawn here because the code
// holds a token: no third-party QR service ever sees it. It is not audited:
// it changes nothing, and the token must stay out of every log.
package world

import (
	"crypto/sha256"
	"encoding/json"
	"net/url"
	"strings"

	"github.com/gofiber/fiber/v3"
	"rsc.io/qr"
)

func connectCode(origin, token string) (string, error) {
	o, err := ParsePushRelay(origin) // the same rule: an https origin (loopback http)
	if err != nil || o == "" {
		return "", ErrBadRequest("origin must be the https address of this server")
	}
	q := url.Values{}
	q.Set("u", o)
	q.Set("t", token)
	return "aiagent://connect?" + q.Encode(), nil
}

func (s *Service) handleConnectQR(c fiber.Ctx) error {
	var body struct {
		Origin string `json:"origin"`
		Token  string `json:"token"`
	}
	if err := json.Unmarshal(c.Body(), &body); err != nil {
		return s.fail(c, ErrBadRequest(`expected {"origin": "https://…"}`))
	}
	own := strings.Clone(c.Get(TokenHeader))
	token := own
	if body.Token != "" && body.Token != own {
		// only an admin draws another token's code: one they just made
		if identity(c).Role != RoleAdmin {
			return c.Status(fiber.StatusForbidden).JSON(failEnv("AIAGENT_ADMIN_ONLY", "ต้องใช้ token ของ admin"))
		}
		if _, ok := s.Store.IdentifyHash(sha256.Sum256([]byte(body.Token))); !ok {
			return s.fail(c, ErrBadRequest("token นี้ไม่ใช่ของ server นี้"))
		}
		token = body.Token
	}
	code, err := connectCode(body.Origin, token)
	if err != nil {
		return s.fail(c, err)
	}
	img, err := qr.Encode(code, qr.M)
	if err != nil {
		return s.fail(c, err)
	}
	img.Scale = 6
	c.Set("Cache-Control", "no-store")
	c.Set("X-Content-Type-Options", "nosniff")
	c.Set("Content-Type", "image/png")
	return c.Send(img.PNG())
}
