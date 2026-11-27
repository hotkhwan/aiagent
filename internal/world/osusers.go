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
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/gofiber/fiber/v3"

	"github.com/hotkhwan/aiagent/internal/audit"
)

// AsHeader lets an admin act inside one user's instance.
const AsHeader = "X-Aiagent-As"

var (
	osUserRE = regexp.MustCompile(`^[a-z][a-z0-9-]{1,30}$`)
	// the link a room's instance answers POST /reviews/:id/view with
	reviewTicketRE = regexp.MustCompile(`^/aiagent/review-view/([0-9a-f]{16,128})$`)
)

// OSUsers is nil unless the service was started with --os-users.
type OSUsers struct {
	SocketDir string // /run/aiagent-users, root only
	TokenDir  string // <state>/instances, root only
	ModelDir  string // /run/aiagent-room, readable by rooms
	Runner    Runner
}

func (o *OSUsers) unit(ctx context.Context, action, name string) error {
	if !osUserRE.MatchString(name) {
		return ErrBadRequest("ชื่อผู้ใช้: a-z 0-9 และขีดกลาง ขึ้นต้นด้วยตัวอักษร")
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
		return 0, nil, nil, ErrBadRequest("ชื่อผู้ใช้ไม่ถูกต้อง")
	}
	tok, err := os.ReadFile(filepath.Join(o.TokenDir, name+".token"))
	if err != nil {
		return 0, nil, nil, &APIError{502, "AIAGENT_INSTANCE_DOWN", "ห้องของผู้ใช้นี้ยังตั้งค่าไม่เสร็จ"}
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
		return 0, nil, nil, &APIError{502, "AIAGENT_INSTANCE_DOWN", "ห้องของผู้ใช้นี้ไม่ตอบ"}
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxForwardBody))
	if err != nil {
		return 0, nil, nil, &APIError{502, "AIAGENT_INSTANCE_DOWN", "ห้องของผู้ใช้นี้ไม่ตอบ"}
	}
	return resp.StatusCode, resp.Header, b, nil
}

// forward answers an API request from a user's instance.
func (s *Service) forward(c fiber.Ctx, name string) error {
	// a user's typed text was checked in the gate (usertext.go)
	if m := sessionPathRE.FindStringSubmatch(strings.TrimPrefix(c.Path(), apiPrefix)); m != nil && identity(c).Role == RoleUser {
		if mate := s.teammateOf(c.Context(), name, m[1]); mate != "" {
			tail := strings.TrimPrefix(strings.TrimPrefix(c.Path(), apiPrefix), m[0])
			if c.Method() != "GET" || (tail != "" && tail != "/messages") {
				return c.Status(fiber.StatusForbidden).JSON(failEnv("AIAGENT_READ_ONLY", "session ของเพื่อนร่วมทีมอ่านได้อย่างเดียว"))
			}
			name = mate
		}
	}
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
		b = s.withOthers(c, name, b)
	}
	// The room hands out a one-load link to a review page; the frame asks
	// this service for it, so it must know which room the link belongs to.
	if t := roomReviewTicket(c.Method(), path, status, b); t != "" {
		s.mu.Lock()
		if s.userTickets == nil {
			s.userTickets = map[string]userTicket{}
		}
		for k, v := range s.userTickets {
			if time.Now().After(v.expires) {
				delete(s.userTickets, k)
			}
		}
		s.userTickets[t] = userTicket{name: name, expires: time.Now().Add(2 * time.Minute)}
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

// roomReviewTicket is the ticket in a room's answer to POST
// /reviews/:id/view ({"details":{"url":"/aiagent/review-view/<ticket>"}}),
// or "" for any other answer.
func roomReviewTicket(method, path string, status int, b []byte) string {
	if method != "POST" || status != 200 || !strings.HasPrefix(path, "/reviews/") || !strings.HasSuffix(path, "/view") {
		return ""
	}
	var env struct {
		Details struct {
			URL string `json:"url"`
		} `json:"details"`
	}
	if json.Unmarshal(b, &env) != nil {
		return ""
	}
	m := reviewTicketRE.FindStringSubmatch(env.Details.URL)
	if m == nil {
		return ""
	}
	return m[1]
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

// instanceSessions lists one user's own sessions, kept for a few seconds since
// every open tab polls.
func (s *Service) instanceSessions(ctx context.Context, name string) []Session {
	return s.instance(ctx, name).sessions
}

func (s *Service) instance(ctx context.Context, name string) mateEntry {
	s.mates.Lock()
	if e, ok := s.mates.m[name]; ok && time.Since(e.at) < mateTTL {
		s.mates.Unlock()
		return e
	}
	s.mates.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	var env struct {
		Details struct {
			Sessions []Session `json:"sessions"`
			Quotas   []Quota   `json:"quotas"`
		} `json:"details"`
	}
	status, _, b, err := s.OSUsers.do(ctx, name, "GET", apiPrefix+"/snapshot", "", nil)
	if err != nil || status != 200 || json.Unmarshal(b, &env) != nil {
		env.Details.Sessions, env.Details.Quotas = nil, nil
	}
	s.mates.Lock()
	if s.mates.m == nil {
		s.mates.m = map[string]mateEntry{}
	}
	e := mateEntry{at: time.Now(), sessions: env.Details.Sessions, quotas: env.Details.Quotas}
	s.mates.m[name] = e
	s.mates.Unlock()
	return e
}

const mateTTL = 3 * time.Second

type mateEntry struct {
	at       time.Time
	sessions []Session
	quotas   []Quota
}

type mateCache struct {
	sync.Mutex
	m map[string]mateEntry
}

// teammates maps each session another user may read to its owner. A user's
// own sessions and the admin's are never in it, whatever an instance claims:
// instances are their users' own processes.
func (s *Service) teammates(ctx context.Context, name string) ([]Session, map[string]string) {
	st, err := s.Store.Load()
	if err != nil {
		return nil, nil
	}
	taken := map[string]bool{}
	if name != "" {
		for _, ss := range s.instanceSessions(ctx, name) {
			taken[ss.SessionID] = true
		}
	} else {
		// a room cannot stand in for one of the admin's own sessions
		for _, ss := range s.Collector.Snapshot(ctx).Sessions {
			taken[ss.SessionID] = true
		}
	}
	p := s.protections(ctx)
	// A session id two rooms both list belongs to neither: an instance could
	// otherwise claim another room's session and have the admin's message to
	// it sent to itself. Only the first room's copy used to count.
	claimed := map[string]int{}
	for _, u := range st.Users {
		if u.OSUser == "" || u.OSUser == name || u.Hash == "" {
			continue
		}
		seen := map[string]bool{}
		for _, ss := range s.instanceSessions(ctx, u.OSUser) {
			if ss.SessionID != "" && !seen[ss.SessionID] {
				seen[ss.SessionID] = true
				claimed[ss.SessionID]++
			}
		}
	}
	for id, n := range claimed {
		if n > 1 {
			log.Printf("rooms: session %s is listed by %d rooms; routed to none", id, n)
		}
	}
	var list []Session
	owner := map[string]string{}
	for _, u := range st.Users {
		if u.OSUser == "" || u.OSUser == name || u.Hash == "" {
			continue
		}
		for _, ss := range s.instanceSessions(ctx, u.OSUser) {
			if taken[ss.SessionID] || ss.SessionID == "" || p.covers(ss.SessionID) || claimed[ss.SessionID] > 1 {
				continue
			}
			// a room set to admin is on the guard's admin list: other rooms
			// do not read it; the admin does
			if name != "" && p.adminRoom[strings.ToLower(ss.SessionID)] != "" {
				continue
			}
			taken[ss.SessionID] = true
			owner[ss.SessionID] = u.OSUser
			why := "ห้องของ " + u.OSUser + " — อ่านบทสนทนาได้ ส่งข้อความไม่ได้"
			if !lookRE.MatchString(ss.Look) {
				ss.Look = ""
			}
			ss.Owner, ss.RepoID, ss.Protected = u.OSUser, u.OSUser, false
			ss.Workspace, ss.Cwd, ss.Pid = "", "", 0
			// name == "" is the admin, who may type into a room and stop it
			// (the request is sent on to that room); a user only reads.
			if name != "" {
				ss.ReadOnly = true
				ss.MessageVia, ss.StopVia, ss.DispatchVia, ss.Prompt = "", "", "", nil
				ss.MessageBlocked, ss.StopBlocked = why, why
			}
			list = append(list, ss)
		}
	}
	return list, owner
}

func (s *Service) teammateOf(ctx context.Context, name, sessionID string) string {
	_, owner := s.teammates(ctx, name)
	return owner[sessionID]
}

// withOthers adds to a user's own snapshot their teammates' sessions, to read,
// and this service's live protected sessions, existence and state only.
func (s *Service) withOthers(c fiber.Ctx, name string, b []byte) []byte {
	var env struct {
		Code    string                     `json:"code"`
		Details map[string]json.RawMessage `json:"details"`
	}
	var own []json.RawMessage
	if json.Unmarshal(b, &env) != nil || env.Details == nil || json.Unmarshal(env.Details["sessions"], &own) != nil {
		return b
	}
	others, _ := s.teammates(c.Context(), name)
	for _, ss := range s.forRole(identity(c), s.Collector.Snapshot(c.Context())).Sessions {
		if ss.Protected && ss.Live {
			others = append(others, ss)
		}
	}
	for _, ss := range others {
		if j, err := json.Marshal(ss); err == nil {
			own = append(own, j)
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
	return path == "/me" || path == "/push/devices" || path == "/push/devices/remove" || path == "/connect/qr" || path == "/connect/pair" || path == "/connect/pair/cancel" || path == "/connect/devices" || strings.HasPrefix(path, "/connect/devices/") || path == "/preview/ticket" || path == "/vault" || strings.HasPrefix(path, "/vault/") || strings.HasPrefix(path, "/guides/") || path == "/rooms" || path == "/tokens" || strings.HasPrefix(path, "/tokens/") ||
		// photo faces are per token, the same whichever room the phone is in
		path == "/faces" || strings.HasPrefix(path, "/faces/")
}

// forwardAudited records a write before it leaves, as mutate does.
func (s *Service) forwardAudited(c fiber.Ctx, name string) error {
	if c.Method() != "GET" {
		rec := audit.Record{Event: "intent", RPC: "aiagent.forward", AdminSub: subject(c), ParamsHash: paramsHash(name, c.Path())}
		if err := s.Auditor.Write(rec); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(failEnv("AIAGENT_AUDIT_FAILED", "เขียนบันทึก audit ไม่ได้ จึงไม่ได้ทำคำสั่งนี้"))
		}
	}
	return s.forward(c, name)
}
