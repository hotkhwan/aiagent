// internal/world/osusers.go
//
// One OS user per user token. The admin's service (root, --os-users) never
// reads a user's files itself: each OS user has their own aiagent instance
// running as that user, reached over a root-owned unix socket, and a user's
// requests are forwarded to it. A user's agents can therefore touch only that
// user's files, and nothing a user leaves in their home is ever opened by root.
//
// Accounts are made and removed by packaging/osuser.sh, started through two
// systemd template units with a fixed argv, because this service's sandbox may
// not do it. Removing an account archives its home first.
package world

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"

	"github.com/hotkhwan/aiagent/internal/audit"
)

// AsHeader lets an admin act inside one user's instance.
const AsHeader = "X-Aiagent-As"

var (
	osUserRE = regexp.MustCompile(`^[a-z][a-z0-9-]{1,30}$`)
	ticketRE = regexp.MustCompile(`"ticket":"([0-9a-f]{16,128})"`)
)

// OSUsers is nil unless the service was started with --os-users.
type OSUsers struct {
	SocketDir string // /run/aiagent-users, root only
	TokenDir  string // <state>/instances, root only
	Runner    Runner
}

func (o *OSUsers) unit(ctx context.Context, action, name string) error {
	if !osUserRE.MatchString(name) {
		return ErrBadRequest("user name must be lower-case letters, digits or dashes, starting with a letter")
	}
	ctx, cancel := context.WithTimeout(ctx, 50*time.Second)
	defer cancel()
	out, err := o.Runner.Run(ctx, "systemctl", "start", "aiagent-osuser-"+action+"@"+name+".service")
	if err != nil {
		return &APIError{502, "AIAGENT_OSUSER_FAILED", "could not " + action + " the OS user: " + truncateRunes(strings.TrimSpace(string(out)), 300)}
	}
	return nil
}

func (o *OSUsers) client(name string) *http.Client {
	sock := filepath.Join(o.SocketDir, name+".sock")
	return &http.Client{
		Timeout: 55 * time.Second,
		Transport: &http.Transport{
			DisableKeepAlives: true,
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", sock)
			},
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

const maxForwardBody = 32 << 20

// do sends one request to a user's instance.
func (o *OSUsers) do(ctx context.Context, name, method, uri, contentType string, body []byte) (int, http.Header, []byte, error) {
	if !osUserRE.MatchString(name) {
		return 0, nil, nil, ErrBadRequest("bad user name")
	}
	tok, err := os.ReadFile(filepath.Join(o.TokenDir, name+".token"))
	if err != nil {
		return 0, nil, nil, &APIError{502, "AIAGENT_INSTANCE_DOWN", "this user's workspace is not set up"}
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://instance"+uri, bytes.NewReader(body))
	if err != nil {
		return 0, nil, nil, err
	}
	req.Header.Set(TokenHeader, strings.TrimSpace(string(tok)))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := o.client(name).Do(req)
	if err != nil {
		return 0, nil, nil, &APIError{502, "AIAGENT_INSTANCE_DOWN", "this user's workspace is not answering"}
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxForwardBody))
	if err != nil {
		return 0, nil, nil, &APIError{502, "AIAGENT_INSTANCE_DOWN", "this user's workspace is not answering"}
	}
	return resp.StatusCode, resp.Header, b, nil
}

// forward answers an API request from a user's instance.
func (s *Service) forward(c fiber.Ctx, name string) error {
	status, hdr, b, err := s.OSUsers.do(c.Context(), name, c.Method(), c.OriginalURL(), c.Get("Content-Type"), c.Body())
	if err != nil {
		return s.fail(c, err)
	}
	// The instance is the user's own process: a 401 or 429 from it must not
	// sign the browser out of this service.
	if status == fiber.StatusUnauthorized {
		status = fiber.StatusBadGateway
	}
	path := strings.TrimPrefix(c.Path(), apiPrefix)
	if status == 200 && path == "/snapshot" && identity(c).Role == RoleUser {
		b = s.withAdminPresence(c, b)
	}
	if m := ticketRE.FindSubmatch(b); m != nil && c.Method() == "POST" && strings.HasPrefix(path, "/reviews/") {
		s.mu.Lock()
		if s.userTickets == nil {
			s.userTickets = map[string]userTicket{}
		}
		for k, v := range s.userTickets {
			if time.Now().After(v.expires) {
				delete(s.userTickets, k)
			}
		}
		s.userTickets[string(m[1])] = userTicket{name: name, expires: time.Now().Add(2 * time.Minute)}
		s.mu.Unlock()
	}
	// Only these types: an instance must not be able to serve a page or a
	// script from this origin.
	ct := hdr.Get("Content-Type")
	if !strings.HasPrefix(ct, "application/json") && ct != "image/png" && ct != "image/jpeg" && ct != "image/jpg" && ct != "image/gif" && ct != "image/webp" {
		ct = "application/octet-stream"
	}
	c.Set("Content-Type", ct)
	c.Set("Cache-Control", "no-store")
	return c.Status(status).Send(b)
}

type userTicket struct {
	name    string
	expires time.Time
}

// userReview serves a review page that lives in a user's instance, in the
// same opaque-origin frame as any other.
func (s *Service) userReview(c fiber.Ctx, ticket string) (bool, error) {
	if s.OSUsers == nil {
		return false, nil
	}
	s.mu.Lock()
	v, ok := s.userTickets[ticket]
	delete(s.userTickets, ticket)
	s.mu.Unlock()
	if !ok || time.Now().After(v.expires) {
		return false, nil
	}
	status, _, b, err := s.OSUsers.do(c.Context(), v.name, "GET", "/aiagent/review-view/"+ticket, "", nil)
	if err != nil || status != 200 {
		return true, hidden(c)
	}
	c.Set("Content-Security-Policy", reviewViewCSP)
	c.Set("X-Content-Type-Options", "nosniff")
	c.Set("Referrer-Policy", "no-referrer")
	c.Set("X-Frame-Options", "SAMEORIGIN")
	c.Set("Cache-Control", "no-store")
	c.Status(fiber.StatusOK).Type("html")
	return true, c.Send(b)
}

// withAdminPresence adds this service's live protected sessions, existence and
// state only, to a user's own snapshot.
func (s *Service) withAdminPresence(c fiber.Ctx, b []byte) []byte {
	var env struct {
		Code    string                     `json:"code"`
		Details map[string]json.RawMessage `json:"details"`
	}
	var own []json.RawMessage
	if json.Unmarshal(b, &env) != nil || env.Details == nil || json.Unmarshal(env.Details["sessions"], &own) != nil {
		return b
	}
	for _, ss := range s.forRole(identity(c), s.Collector.Snapshot(c.Context())).Sessions {
		if ss.Protected && ss.Live {
			if j, err := json.Marshal(ss); err == nil {
				own = append(own, j)
			}
		}
	}
	j, err := json.Marshal(own)
	if err != nil {
		return b
	}
	env.Details["sessions"] = j
	out, err := json.Marshal(env)
	if err != nil {
		return b
	}
	return out
}

// localPaths are answered here even while a request is for a user's instance.
func localPath(path string) bool {
	return path == "/me" || path == "/tokens" || strings.HasPrefix(path, "/tokens/")
}

// forwardAudited records a write before it leaves, as mutate does.
func (s *Service) forwardAudited(c fiber.Ctx, name string) error {
	if c.Method() != "GET" {
		rec := audit.Record{Event: "intent", RPC: "aiagent.forward", AdminSub: subject(c), ParamsHash: paramsHash(name, c.Path())}
		if err := s.Auditor.Write(rec); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(failEnv("AIAGENT_AUDIT_FAILED", "audit log unavailable — action not performed"))
		}
	}
	return s.forward(c, name)
}
