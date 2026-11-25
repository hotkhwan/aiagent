// internal/world/preview.go
//
// A room's page at https://<preview host>/dev/<room>/: what that room's agent
// is building, for that room's user and the admin.
//
// The content is written by a room and is served from an origin that also
// serves other applications, so it must never run as that origin. Two layers:
//
//   - /dev/<room>/ is a small shell owned by this service. It frames the
//     content in an iframe sandboxed without allow-same-origin.
//   - every content response carries a CSP sandbox without allow-same-origin
//     too, so opening its address directly gives it nothing more.
//
// A sandboxed document sends no SameSite cookie, so content is addressed by a
// capability in its path: 192 random bits, one per viewer session, bound to
// one room, and dead soon after the shell that holds it closes.
//
// All of this answers on its own loopback listener, which answers only for the
// preview host. The main listener refuses that host (http.go), so neither
// surface is reachable through the other.
package world

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/user"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gofiber/fiber/v3"

	"github.com/hotkhwan/aiagent/internal/audit"
)

// reservedRooms are path segments next to the rooms' own under /dev/.
// RESERVED in preview.conf adds to them.
var reservedRooms = map[string]bool{
	"ci": true, "cd": true, "api": true, "kapi": true, "events": true, "gateway": true, "dev": true, "admin": true,
	"assets": true, "static": true, "ws": true, "login": true, "health": true,
}

const (
	previewCookie     = "__Secure-aiagent-preview"
	previewTicketTTL  = time.Minute
	previewSessionTTL = 8 * time.Hour
	// the shell asks for state every few seconds; a background tab at least
	// every minute
	previewIdleTTL     = 10 * time.Minute
	previewMaxSessions = 1000
	previewStateTTL    = 700 * time.Millisecond
	previewMaxStreams  = 64 // per room, so one page cannot use every connection
)

// PreviewConfig is /etc/aiagent/preview.conf, KEY=VALUE lines.
type PreviewConfig struct {
	Host     string // the only host the preview answers on
	Listen   string // loopback address of the preview listener
	Chromium string // optional browser for captures
	Reserved []string
}

var previewHostRE = regexp.MustCompile(`^[a-z0-9]([a-z0-9.-]{0,251}[a-z0-9])?$`)

// LoadPreviewConfig reads the file; a missing file or an empty HOST is off.
func LoadPreviewConfig(path string) (*PreviewConfig, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	cfg := &PreviewConfig{Listen: "127.0.0.1:8891"}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		k, v, ok := strings.Cut(strings.TrimSpace(sc.Text()), "=")
		if !ok || strings.HasPrefix(k, "#") {
			continue
		}
		switch v = strings.TrimSpace(v); strings.TrimSpace(k) {
		case "HOST":
			cfg.Host = strings.ToLower(v)
		case "LISTEN":
			if v != "" {
				cfg.Listen = v
			}
		case "CHROMIUM":
			cfg.Chromium = v
		case "RESERVED":
			cfg.Reserved = strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == ' ' })
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if cfg.Host == "" && len(cfg.Reserved) == 0 {
		return nil, nil
	}
	if cfg.Host != "" {
		if !previewHostRE.MatchString(cfg.Host) {
			return nil, fmt.Errorf("HOST is not a host name")
		}
		host, _, err := net.SplitHostPort(cfg.Listen)
		if ip := net.ParseIP(host); err != nil || ip == nil || !ip.IsLoopback() {
			return nil, fmt.Errorf("LISTEN must be a loopback address and port")
		}
	}
	return cfg, nil
}

// Preview serves the rooms' pages.
type Preview struct {
	cfg      PreviewConfig
	svc      *Service
	reserved map[string]bool
	now      func() time.Time
	procRoot string
	uidOf    func(room string) (uint32, error)
	capture  *capturer

	mu       sync.Mutex
	tickets  map[[32]byte]pvTicket
	sessions map[[32]byte]*pvSession
	caps     map[[32]byte]*pvSession
	states   map[string]pvState
	streams  map[string]int
}

// pvGrant is why a viewer may see a room: checked again on every request, so
// it ends when the room is revoked or its token (or the admin's) is replaced.
type pvGrant struct {
	room, roomID, roomHash string
	adminHash              string // set for an admin
	who                    string // for the audit log
}

type pvTicket struct {
	grant   pvGrant
	expires time.Time
}

type pvSession struct {
	grant         pvGrant
	created, seen time.Time
	cap           string
	capHash       [32]byte
	shotID        string
	shots         []captureImage
	shotAt        time.Time
}

type pvState struct {
	at    time.Time
	state PreviewState
	err   error
}

// NewPreview returns the preview of svc's rooms.
func NewPreview(svc *Service, cfg PreviewConfig) *Preview {
	p := &Preview{
		cfg: cfg, svc: svc, now: time.Now, procRoot: "/proc", uidOf: roomUID, reserved: map[string]bool{},
		tickets: map[[32]byte]pvTicket{}, sessions: map[[32]byte]*pvSession{}, caps: map[[32]byte]*pvSession{},
		states: map[string]pvState{}, streams: map[string]int{},
	}
	for k := range reservedRooms {
		p.reserved[k] = true
	}
	for _, k := range cfg.Reserved {
		p.reserved[strings.ToLower(k)] = true
	}
	return p
}

// Room uids are the ones packaging/osuser.sh hands out. A name that resolves
// outside them is not a room, and its listeners are not a room's.
const roomUIDMin, roomUIDMax = 700000, 799999

func roomUID(room string) (uint32, error) {
	u, err := user.Lookup(room)
	if err != nil {
		return 0, err
	}
	n, err := strconv.ParseUint(u.Uid, 10, 32)
	if err != nil || n < roomUIDMin || n > roomUIDMax {
		return 0, fmt.Errorf("not a room's uid")
	}
	return uint32(n), nil
}

func (s *Service) roomReserved(name string) bool {
	if s.Preview != nil {
		return s.Preview.reserved[name]
	}
	return reservedRooms[name]
}

// on reports whether pages are served; a config may only reserve names.
func (p *Preview) on() bool { return p != nil && p.cfg.Host != "" }

// onHost reports whether a Host header names the preview host.
func (p *Preview) onHost(host string) bool {
	if !p.on() {
		return false
	}
	host = strings.ToLower(strings.TrimSpace(host))
	if h, port, err := net.SplitHostPort(host); err == nil {
		if _, err := strconv.ParseUint(port, 10, 16); err != nil {
			return false
		}
		host = h
	}
	return strings.TrimSuffix(host, ".") == p.cfg.Host
}

func pvRandom() (string, [32]byte, error) {
	var b [24]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", [32]byte{}, err
	}
	t := hex.EncodeToString(b[:])
	return t, sha256.Sum256([]byte(t)), nil
}

// grantFor decides whether the holder of token may open room.
func (p *Preview) grantFor(id Identity, token, room, who string) (pvGrant, error) {
	refuse := &APIError{403, "AIAGENT_PREVIEW_REFUSED", "this room's page is not yours to open"}
	if !osUserRE.MatchString(room) || p.reserved[room] {
		return pvGrant{}, refuse
	}
	if id.Role != RoleAdmin && (id.OSUser == "" || id.OSUser != room) {
		return pvGrant{}, refuse
	}
	st, err := p.svc.Store.Load()
	if err != nil {
		return pvGrant{}, err
	}
	sum := sha256.Sum256([]byte(token))
	g := pvGrant{room: room, who: who}
	for _, u := range st.Users {
		if u.OSUser == room && u.Hash != "" {
			g.roomID, g.roomHash = u.ID, u.Hash
		}
	}
	if id.Role == RoleAdmin {
		g.adminHash = hex.EncodeToString(sum[:])
	} else if !hashMatches(g.roomHash, sum) {
		return pvGrant{}, refuse
	}
	if !p.holds(g) {
		return pvGrant{}, refuse
	}
	return g, nil
}

// holds reports whether a grant is still good.
func (p *Preview) holds(g pvGrant) bool {
	st, err := p.svc.Store.Load()
	if err != nil || !st.Enabled || st.TokenHash == "" || g.roomHash == "" || p.reserved[g.room] {
		return false
	}
	if g.adminHash != "" && subtle.ConstantTimeCompare([]byte(g.adminHash), []byte(st.TokenHash)) != 1 {
		return false
	}
	for _, u := range st.Users {
		if u.OSUser == g.room && u.ID == g.roomID {
			return subtle.ConstantTimeCompare([]byte(u.Hash), []byte(g.roomHash)) == 1
		}
	}
	return false
}

// sweep drops what has expired. p.mu is held.
func (p *Preview) sweep(now time.Time) {
	for k, t := range p.tickets {
		if now.After(t.expires) {
			delete(p.tickets, k)
		}
	}
	for k, ss := range p.sessions {
		if now.Sub(ss.created) > previewSessionTTL {
			delete(p.sessions, k)
			delete(p.caps, ss.capHash)
		} else if now.Sub(ss.shotAt) > previewIdleTTL {
			ss.shots, ss.shotID = nil, ""
		}
	}
}

// handlePreviewTicket hands the caller a one-time way into one room's page.
func (s *Service) handlePreviewTicket(c fiber.Ctx) error {
	var body struct {
		Room string `json:"room"`
	}
	if err := c.Bind().Body(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(failEnv("AIAGENT_BAD_REQUEST", "invalid JSON body"))
	}
	p := s.Preview
	if s.OSUsers == nil || !p.on() {
		return hidden(c)
	}
	room, token := strings.Clone(body.Room), strings.Clone(c.Get(TokenHeader))
	c.Set("Cache-Control", "no-store")
	return s.mutate(c, "aiagent.previewTicket", []string{room}, func() (any, error) {
		g, err := p.grantFor(identity(c), token, room, subject(c))
		if err != nil {
			return nil, err
		}
		t, key, err := pvRandom()
		if err != nil {
			return nil, err
		}
		now := p.now()
		p.mu.Lock()
		defer p.mu.Unlock()
		p.sweep(now)
		if len(p.tickets) >= previewMaxSessions {
			return nil, ErrConflict("too many open tickets")
		}
		p.tickets[key] = pvTicket{grant: g, expires: now.Add(previewTicketTTL)}
		return fiber.Map{"url": "https://" + p.cfg.Host + "/dev/" + room + "/#t=" + t}, nil
	})
}

// enter exchanges a ticket for a session. A ticket is gone once presented,
// for whatever room.
func (p *Preview) enter(room, ticket string) (string, *pvSession, bool) {
	key := sha256.Sum256([]byte(ticket))
	now := p.now()
	p.mu.Lock()
	t, ok := p.tickets[key]
	delete(p.tickets, key)
	p.mu.Unlock()
	if !ok || now.After(t.expires) || t.grant.room != room || !p.holds(t.grant) {
		return "", nil, false
	}
	cookie, ckey, err := pvRandom()
	if err != nil {
		return "", nil, false
	}
	cp, capKey, err := pvRandom()
	if err != nil {
		return "", nil, false
	}
	ss := &pvSession{grant: t.grant, created: now, seen: now, cap: cp, capHash: capKey}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sweep(now)
	if len(p.sessions) >= previewMaxSessions {
		return "", nil, false
	}
	p.sessions[ckey], p.caps[capKey] = ss, ss
	return cookie, ss, true
}

// viewer is the session a shell request carries for room, if any.
func (p *Preview) viewer(r *http.Request, room string) *pvSession {
	for _, c := range r.Cookies() {
		if c.Name != previewCookie {
			continue
		}
		key := sha256.Sum256([]byte(c.Value))
		p.mu.Lock()
		ss := p.sessions[key]
		p.mu.Unlock()
		if ss == nil || ss.grant.room != room || p.now().Sub(ss.created) > previewSessionTTL {
			continue
		}
		if !p.holds(ss.grant) {
			p.mu.Lock()
			delete(p.sessions, key)
			delete(p.caps, ss.capHash)
			p.mu.Unlock()
			continue
		}
		return ss
	}
	return nil
}

// touch keeps a viewer's capability alive, replacing one that went idle.
func (p *Preview) touch(ss *pvSession) string {
	now := p.now()
	p.mu.Lock()
	defer p.mu.Unlock()
	if now.Sub(ss.seen) > previewIdleTTL {
		if cp, key, err := pvRandom(); err == nil {
			delete(p.caps, ss.capHash)
			ss.cap, ss.capHash = cp, key
			p.caps[key] = ss
		}
	}
	ss.seen = now
	return ss.cap
}

// capable reports whether a capability opens room now. The lookup is by hash
// and the comparison constant-time, so neither says how close a guess came.
func (p *Preview) capable(room, presented string) bool {
	key := sha256.Sum256([]byte(presented))
	now := p.now()
	p.mu.Lock()
	ss := p.caps[key]
	ok := ss != nil &&
		subtle.ConstantTimeCompare([]byte(ss.cap), []byte(presented)) == 1 &&
		subtle.ConstantTimeCompare([]byte(ss.grant.room), []byte(room)) == 1 &&
		now.Sub(ss.seen) <= previewIdleTTL && now.Sub(ss.created) <= previewSessionTTL
	p.mu.Unlock()
	return ok && p.holds(ss.grant)
}

var previewPathRE = regexp.MustCompile(`^/dev/([a-z][a-z0-9-]{1,30})(/.*)?$`)
var previewCapRE = regexp.MustCompile(`^/~([0-9a-f]{48})(/.*)?$`)

func (p *Preview) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	m := previewPathRE.FindStringSubmatch(r.URL.EscapedPath())
	if !p.onHost(r.Host) || m == nil || p.reserved[m[1]] || !p.svc.Store.Enabled() {
		contentError(w, http.StatusNotFound)
		return
	}
	room, rest := m[1], m[2]
	if c := previewCapRE.FindStringSubmatch(rest); c != nil {
		if !p.capable(room, c[1]) {
			contentError(w, http.StatusNotFound)
			return
		}
		// a stream ends when its capability does
		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()
		go func() {
			t := time.NewTicker(2 * time.Second)
			defer t.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-t.C:
					if !p.capable(room, c[1]) {
						cancel()
						return
					}
				}
			}
		}()
		p.content(w, r.WithContext(ctx), room, "/dev/"+room+"/~"+c[1]+"/")
		return
	}
	if strings.HasPrefix(rest, "/~") {
		contentError(w, http.StatusNotFound)
		return
	}
	p.shell(w, r, room, rest)
}

// ---------- the shell ----------

func (p *Preview) shellHeaders(w http.ResponseWriter, r *http.Request, room string) {
	// Paths, not 'self': the rest of this origin is other applications, and a
	// room's own content is under the same one.
	own := r.Host + "/dev/" + room + "/-/"
	h := w.Header()
	h.Set("Content-Security-Policy", "default-src 'none'; script-src "+own+"; style-src "+own+"; connect-src "+own+
		"; img-src data:; frame-src "+r.Host+"/dev/"+room+"/; frame-ancestors 'none'; base-uri 'none'; form-action 'none'; require-trusted-types-for 'script'; trusted-types 'none'")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Frame-Options", "DENY")
	h.Set("Cross-Origin-Opener-Policy", "same-origin")
	h.Set("Cross-Origin-Resource-Policy", "same-origin")
	h.Set("Cache-Control", "no-store")
}

func (p *Preview) shell(w http.ResponseWriter, r *http.Request, room, rest string) {
	p.shellHeaders(w, r, room)
	get := r.Method == http.MethodGet || r.Method == http.MethodHead
	file := map[string][2]string{
		"/": {"shell.html", "text/html; charset=utf-8"}, "/-/shell.js": {"shell.js", "text/javascript; charset=utf-8"}, "/-/shell.css": {"shell.css", "text/css; charset=utf-8"},
	}[rest]
	switch {
	case rest == "" && get:
		w.Header().Set("Location", "/dev/"+room+"/")
		w.WriteHeader(http.StatusPermanentRedirect)
	case file[0] != "" && get:
		b, err := fs.ReadFile(uiFS, "preview/"+file[0])
		if err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", file[1])
		_, _ = w.Write(b)
	case rest == "/-/enter" && r.Method == http.MethodPost:
		p.shellEnter(w, r, room)
	case rest == "/-/state" && get:
		p.shellState(w, r, room)
	case rest == "/-/capture" && r.Method == http.MethodPost:
		p.shellCapture(w, r, room)
	case rest == "/-/review" && r.Method == http.MethodPost:
		p.shellReview(w, r, room)
	default:
		pvJSON(w, http.StatusNotFound, failEnv("NOT_FOUND", "not found"))
	}
}

func pvJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func pvFail(w http.ResponseWriter, err error) {
	var ae *APIError
	if errors.As(err, &ae) {
		pvJSON(w, ae.Status, failEnv(ae.Code, ae.Msg))
		return
	}
	pvJSON(w, http.StatusInternalServerError, failEnv("AIAGENT_INTERNAL", "internal error"))
}

// fromShell refuses a shell call made by the framed content (its origin is
// "null") or by another site. Another application on this same origin cannot
// be told apart here; it is as trusted as the origin.
func (p *Preview) fromShell(r *http.Request) bool {
	if r.Header.Get("X-Aiagent-Preview") != "1" {
		return false
	}
	if v := r.Header.Get("Sec-Fetch-Site"); v != "" && v != "same-origin" {
		return false
	}
	o := r.Header.Get("Origin")
	if o == "" {
		return r.Method == http.MethodGet
	}
	return strings.EqualFold(strings.TrimPrefix(o, "https://"), r.Host) && strings.HasPrefix(o, "https://")
}

func (p *Preview) authed(w http.ResponseWriter, r *http.Request, room string) *pvSession {
	if !p.fromShell(r) {
		pvJSON(w, http.StatusForbidden, failEnv("AIAGENT_PREVIEW_REFUSED", "not from this page"))
		return nil
	}
	ss := p.viewer(r, room)
	if ss == nil {
		pvJSON(w, http.StatusUnauthorized, failEnv("AIAGENT_PREVIEW_SIGNIN", "open this page from aiagent"))
	}
	return ss
}

func (p *Preview) audit(event, rpc string, g pvGrant, err error) error {
	rec := audit.Record{Event: event, RPC: rpc, AdminSub: g.who, ParamsHash: paramsHash(g.room)}
	if event == "result" {
		rec.Result = "ok"
		if err != nil {
			rec.Result, rec.Err = "error", err.Error()
		}
	}
	return p.svc.Auditor.Write(rec)
}

func (p *Preview) shellEnter(w http.ResponseWriter, r *http.Request, room string) {
	var body struct {
		Ticket string `json:"ticket"`
	}
	if !p.fromShell(r) || json.NewDecoder(io.LimitReader(r.Body, 1024)).Decode(&body) != nil {
		pvJSON(w, http.StatusForbidden, failEnv("AIAGENT_PREVIEW_REFUSED", "not from this page"))
		return
	}
	cookie, ss, ok := p.enter(room, body.Ticket)
	if !ok {
		pvJSON(w, http.StatusUnauthorized, failEnv("AIAGENT_PREVIEW_SIGNIN", "this link was used or has expired; open the page from aiagent again"))
		return
	}
	if err := p.audit("result", "aiagent.previewEnter", ss.grant, nil); err != nil {
		pvJSON(w, http.StatusInternalServerError, failEnv("AIAGENT_AUDIT_FAILED", "audit log unavailable"))
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: previewCookie, Value: cookie, Path: "/dev/" + room, MaxAge: int(previewSessionTTL / time.Second),
		HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode,
	})
	pvJSON(w, http.StatusOK, okEnv(nil))
}

func (p *Preview) shellState(w http.ResponseWriter, r *http.Request, room string) {
	ss := p.authed(w, r, room)
	if ss == nil {
		return
	}
	out := map[string]any{"room": room, "base": "/dev/" + room + "/~" + p.touch(ss) + "/", "capture": p.capture != nil}
	st, err := p.state(r.Context(), room)
	if err != nil {
		st = PreviewState{Kind: "none", Error: "down"}
	}
	if st.Kind == "port" {
		// the page reloads when its server comes up
		st.Version = "up"
		uid, err := p.uidOf(room)
		if err == nil {
			_, err = roomListener(p.procRoot, st.Port, uid)
		}
		if err != nil {
			st.Version = "down"
		}
	}
	out["source"] = st
	pvJSON(w, http.StatusOK, okEnv(out))
}

// state asks the room's own instance what it shows. The answer is the room's
// word: the port is checked again before anything connects to it.
func (p *Preview) state(ctx context.Context, room string) (PreviewState, error) {
	p.mu.Lock()
	if e, ok := p.states[room]; ok && time.Since(e.at) < previewStateTTL {
		p.mu.Unlock()
		return e.state, e.err
	}
	p.mu.Unlock()
	cctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	var env struct {
		Details PreviewState `json:"details"`
	}
	status, _, b, err := p.svc.OSUsers.do(cctx, room, "GET", apiPrefix+"/preview/state", "", nil)
	switch {
	case err != nil:
	case status != 200:
		err = ErrNotFound("this room's instance does not serve a preview yet")
	case json.Unmarshal(b, &env) != nil:
		err = ErrNotFound("bad answer")
	}
	st := env.Details
	st.Folder, st.Error, st.Version = truncateRunes(st.Folder, 200), truncateRunes(st.Error, 200), truncateRunes(st.Version, 64)
	if err == nil && st.Kind == "port" && !previewPortOK(st.Port) {
		st = PreviewState{Kind: "none", Error: "bad port"}
	}
	if ctx.Err() == nil {
		p.mu.Lock()
		p.states[room] = pvState{at: time.Now(), state: st, err: err}
		p.mu.Unlock()
	}
	return st, err
}

func (p *Preview) shellCapture(w http.ResponseWriter, r *http.Request, room string) {
	ss := p.authed(w, r, room)
	if ss == nil {
		return
	}
	if p.capture == nil {
		pvJSON(w, http.StatusNotFound, failEnv("AIAGENT_NO_CAPTURE", "capture needs a browser on this machine"))
		return
	}
	if err := p.audit("intent", "aiagent.previewCapture", ss.grant, nil); err != nil {
		pvJSON(w, http.StatusInternalServerError, failEnv("AIAGENT_AUDIT_FAILED", "audit log unavailable — action not performed"))
		return
	}
	shots, err := p.capture.run(r.Context(), room, func(base string) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !strings.HasPrefix(r.URL.EscapedPath(), base) || !p.holds(ss.grant) {
				contentError(w, http.StatusNotFound)
				return
			}
			p.content(w, r, room, base)
		})
	})
	_ = p.audit("result", "aiagent.previewCapture", ss.grant, err)
	if err != nil {
		pvFail(w, err)
		return
	}
	id, _, err := pvRandom()
	if err != nil {
		pvFail(w, err)
		return
	}
	p.mu.Lock()
	ss.shots, ss.shotID, ss.shotAt = shots, id, p.now()
	p.mu.Unlock()
	pvJSON(w, http.StatusOK, okEnv(map[string]any{"id": id, "images": shots}))
}

func (p *Preview) shellReview(w http.ResponseWriter, r *http.Request, room string) {
	ss := p.authed(w, r, room)
	if ss == nil {
		return
	}
	var body struct {
		ID   string `json:"id"`
		Note string `json:"note"`
	}
	if json.NewDecoder(io.LimitReader(r.Body, 16<<10)).Decode(&body) != nil || len(body.Note) > 4000 {
		pvFail(w, ErrBadRequest("invalid JSON body"))
		return
	}
	p.mu.Lock()
	shots, ok := ss.shots, ss.shotID != "" && subtle.ConstantTimeCompare([]byte(ss.shotID), []byte(body.ID)) == 1
	if ok {
		ss.shots, ss.shotID = nil, "" // one capture, one page
	}
	p.mu.Unlock()
	if !ok {
		pvFail(w, ErrConflict("capture the page first"))
		return
	}
	b, _ := json.Marshal(previewReviewBody{Note: body.Note, Images: shots})
	if err := p.audit("intent", "aiagent.previewReview", ss.grant, nil); err != nil {
		pvJSON(w, http.StatusInternalServerError, failEnv("AIAGENT_AUDIT_FAILED", "audit log unavailable — action not performed"))
		return
	}
	var env struct {
		Details struct {
			ReviewID string `json:"reviewId"`
		} `json:"details"`
	}
	status, _, rb, err := p.svc.OSUsers.do(r.Context(), room, "POST", apiPrefix+"/preview/review", "application/json", b)
	if err == nil && (status != 200 || json.Unmarshal(rb, &env) != nil || !reviewIDRE.MatchString(env.Details.ReviewID)) {
		err = &APIError{502, "AIAGENT_PREVIEW_REVIEW", "the room did not take the review page"}
	}
	_ = p.audit("result", "aiagent.previewReview", ss.grant, err)
	if err != nil {
		pvFail(w, err)
		return
	}
	p.svc.roomRev.Lock()
	p.svc.roomRev.at = time.Time{}
	p.svc.roomRev.Unlock()
	pvJSON(w, http.StatusOK, okEnv(map[string]any{"reviewId": env.Details.ReviewID}))
}

// Server is the HTTP server of the preview listener. Streams (hot reload) have
// no write deadline; their capability ends them.
func (p *Preview) Server() *http.Server {
	return &http.Server{Handler: p, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute, MaxHeaderBytes: 64 << 10}
}

// Listen serves the preview on its loopback address until it fails.
func (p *Preview) Listen() error {
	ln, err := net.Listen("tcp", p.cfg.Listen)
	if err != nil {
		return err
	}
	return p.Server().Serve(ln)
}
