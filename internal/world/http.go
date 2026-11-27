// internal/world/http.go
//
// Routes (mounted on this service's own listener, loopback by default):
//
//	GET  /aiagent                      the AI Agent World page (404 while disabled)
//	GET  /aiagent/api/snapshot         token
//	POST /aiagent/api/preview/ticket   token — {room} → one-time link to a room's page
//	GET  /aiagent/api/sessions/:id     token — session + tool timeline
//	GET  /aiagent/api/runs/:id/log     token — agcmd eng run notes (tail)
//	POST /aiagent/api/missions         token — {workspace, task}   → agcmd eng
//	POST /aiagent/api/messages         token — {workspace, message}→ agcmd dispatch
//	POST /aiagent/api/runs/:id/answer  token — {answer}            → agcmd eng answer
//	POST /aiagent/api/runs/:id/approve token                       → agcmd eng approve
//	POST /aiagent/api/runs/:id/abort   token                       → agcmd eng abort
//	GET  /aiagent/api/sessions/:id/messages token — redacted user/assistant text
//	POST /aiagent/api/sessions/:id/message  token — {message} → that session
//	POST /aiagent/api/sessions/:id/answer   token — {option: n} one choice, or {ask: {tabs,
//	                                    answers, submit, chat}} a whole AskUserQuestion (ask.go)
//	                                   (agcmd dispatch, or relay for own bg sessions)
//	POST /aiagent/api/connect/pair     token — {origin} → one-time pairing code + QR for the phone app (pair.go)
//	POST /aiagent/api/connect/pair/cancel token — ends the caller's pairing codes (pair.go)
//	GET  /aiagent/api/connect/devices     token — paired phones in the caller's scope (devices.go)
//	POST /aiagent/api/connect/devices/:id/revoke token — end one paired phone (devices.go)
//	POST /aiagent/pair                 no token — {code} → the token, once, within 10 minutes (pair.go)
//	POST /aiagent/api/tokens/:id/pair  admin — {token, origin} → one-time pairing QR for a user token it holds (pair.go)
//	POST /aiagent/login-qr             no token — {origin} → a QR the app scans to sign this browser in (loginqr.go)
//	GET  /aiagent/login-qr/:id         no token, nonce header — pending, or the browser's new token once (loginqr.go)
//	POST /aiagent/api/login-qr/:id/info|approve token — the app sees who asks, then approves (loginqr.go)
//	POST /aiagent/api/push/devices     token — {handle} from the phone app's relay (push.go)
//	POST /aiagent/api/uploads          token — raw image or zip body → {path} for a message
//	POST /aiagent/api/sessions/:id/model token — /model <opus|sonnet|haiku> via the session's channel
//	POST /aiagent/api/sessions/:id/stop token — claude stop <id> (own bg session)
//	                                   or agcmd restart <ws> (CLI in a agcmd pane);
//	                                   interactive/foreign sessions are refused
//	POST /aiagent/api/sessions/:id/resume token — start a stopped session on its
//	                                   own conversation, in the background
//	                                   (claude --bg --resume); nothing typed
//
// The on/off switch and the token live in the CLI, not on this listener.
package world

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gofiber/fiber/v3"

	"github.com/hotkhwan/aiagent/internal/audit"
	"github.com/hotkhwan/aiagent/internal/orchestrator"
	"github.com/hotkhwan/aiagent/internal/planner"
	"github.com/hotkhwan/aiagent/internal/taskstore"
	"github.com/hotkhwan/aiagent/web"
)

// TokenHeader carries the AI Agent World access token.
const TokenHeader = "X-Aiagent-Token"

// uiFS is the embedded browser UI. It lives in its own root package so the
// tree sits at web/ rather than buried inside this package: //go:embed cannot
// reach a parent directory, so the embed declaration has to live beside the
// files it embeds.
var uiFS = web.FS

type envelope struct {
	Code    string `json:"code"`
	Message string `json:"message,omitempty"`
	Details any    `json:"details,omitempty"`
}

func okEnv(d any) envelope            { return envelope{Code: "SUCCESS", Details: d} }
func failEnv(code, m string) envelope { return envelope{Code: code, Message: m} }

// Service wires the switch, the collector and the audit log to HTTP.
type Service struct {
	Store     *Store
	Collector *Collector
	Auditor   *audit.Logger
	// Tasks is the PM's task log; nil means the task routes are absent.
	Tasks *taskstore.Store
	// Orch dispatches and follows tasks; nil means dispatch is off.
	Orch *orchestrator.Orchestrator
	// PM is the central planner and PMThread its conversation log; with either
	// nil the PM routes are absent and review pages offer no second opinion.
	PM       PMAgent
	PMThread *planner.Thread

	// OSUsers is set when each user token gets its own OS user (osusers.go).
	OSUsers *OSUsers
	// AllowedHosts, when set, are the only Host names answered besides
	// loopback ones (hosts.go); empty answers every name.
	AllowedHosts []string
	userTickets  map[string]userTicket
	// Preview is set when rooms have pages of their own (preview.go).
	Preview *Preview
	// PushRelay is the relay phone notifications go through (push.go);
	// empty turns them off.
	PushRelay string
	// UpdateRepo is the checkout the web page updates from (selfupdate.go);
	// empty means the one packaging/install.sh recorded.
	UpdateRepo string
	update     updateState
	release    releaseUpdate
	pushSource func(context.Context, Identity) ([]PushItem, error)
	push       pushState
	// Duties records what each room is for (roomduties.go); nil without rooms.
	Duties  *RoomDuties
	roomRev roomReviewCache
	roomCap roomCaptureState
	mates   mateCache

	mu       sync.Mutex
	failures map[string][]time.Time
	// lastWrite is the admin's (every admin token and phone), userWrites each
	// user token's: one user's writes never slow the admin or another user.
	lastWrite  time.Time
	userWrites map[string]time.Time
	// uploads is the admin's limit, userUploads each user token's
	uploads     uploadLimiter
	userUploads map[string]*uploadLimiter
	tickets     map[string]viewTicket
	// sends remembers recent sends so one is never typed twice (sendonce.go)
	sends sendOnce
	// pair holds the phone app's one-time pairing tickets (pair.go)
	pair pairState
	// loginQR holds the browsers waiting to be signed in by the app (loginqr.go)
	loginQR loginQRState

	policyOnce sync.Once

	// bg tracks background PM calls so tests and shutdown can wait for them.
	bg       sync.WaitGroup
	agentsMu sync.Mutex
	agentsAt time.Time
	agents   []planner.AgentBrief
}

// viewTicket lets one iframe load one review page without the API token.
type viewTicket struct {
	reviewID string
	expires  time.Time
}

// NewService returns a service with packaged defaults.
func NewService(store *Store, col *Collector, auditor *audit.Logger) *Service {
	s := &Service{Store: store, Collector: col, Auditor: auditor, failures: map[string][]time.Time{}}
	if col != nil && col.TrustedRoots == nil && store != nil {
		col.TrustedRoots = func() []string { return LoadTrustedRoots(store.dir) }
	}
	if col != nil && col.OnTrust == nil {
		col.OnTrust = s.auditTrust
	}
	return s
}

const (
	maxFailures    = 10
	maxFailureKeys = 4096
	failureWindow  = 5 * time.Minute
	writeSpacing   = time.Second
	securityHeader = "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data: blob:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'"
)

func (s *Service) tooManyFailures(ip string, now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	kept := s.failures[ip][:0]
	for _, t := range s.failures[ip] {
		if now.Sub(t) < failureWindow {
			kept = append(kept, t)
		}
	}
	s.failures[ip] = kept
	return len(kept) >= maxFailures
}

func (s *Service) recordFailure(ip string, now time.Time) {
	s.mu.Lock()
	s.failures[ip] = append(s.failures[ip], now)
	// a full table loses its stalest address, never everyone: wiping it
	// unlocked the guesser who filled it
	evictOldest(s.failures, maxFailureKeys)
	s.mu.Unlock()
}

// evictOldest drops the addresses whose last failure is oldest until m holds
// at most max of them.
func evictOldest(m map[string][]time.Time, max int) {
	for len(m) > max {
		var oldest string
		var at time.Time
		for k, ts := range m {
			last := time.Time{}
			if len(ts) > 0 {
				last = ts[len(ts)-1]
			}
			if oldest == "" || last.Before(at) {
				oldest, at = k, last
			}
		}
		delete(m, oldest)
	}
}

// clientAddr is the address wrong tokens are counted against.
//
// Through the Cloudflare tunnel every request arrives from loopback, so the
// peer address alone would put every visitor in one bucket and let anyone lock
// the owner out. When the peer is loopback, the visitor address the tunnel
// adds (Cf-Connecting-Ip) is used instead. Only loopback may name it: a client
// reaching the service directly is counted by its own address, or it could
// pick a fresh one for every guess.
func clientAddr(c fiber.Ctx) string {
	return visitorKey(c.IP(), c.Get("Cf-Connecting-Ip"), c.Get("X-Forwarded-For"), currentTrustedProxies())
}

// lockoutKey is clientAddr's decision on plain values with no other proxy
// named, so it can be tested without a socket (see proxies.go).
func lockoutKey(peer, cfConnectingIP string) string {
	return visitorKey(peer, cfConnectingIP, "", nil)
}

// hidden answers 404 so a disabled feature is indistinguishable from absent.
func hidden(c fiber.Ctx) error {
	return c.Status(fiber.StatusNotFound).JSON(failEnv("NOT_FOUND", "not found"))
}

func (s *Service) gate(c fiber.Ctx) error {
	if !s.Store.Enabled() {
		return hidden(c)
	}
	// The token is checked before the lockout, and a valid one is always let
	// in: anyone on this machine can name any visitor address (clientAddr),
	// and could otherwise lock the owner out by guessing under theirs. Only
	// wrong tokens are counted.
	who, ok := s.Store.Identify(c.Get(TokenHeader))
	if !ok {
		now := time.Now()
		ip := clientAddr(c)
		if s.tooManyFailures(ip, now) {
			return c.Status(fiber.StatusTooManyRequests).JSON(failEnv("AIAGENT_LOCKED", "ใส่ token ผิดหลายครั้งเกินไป รอสักครู่แล้วลองใหม่"))
		}
		s.recordFailure(ip, now)
		return c.Status(fiber.StatusUnauthorized).JSON(failEnv("AIAGENT_TOKEN_INVALID", "ไม่มี token หรือ token ของ AI Agent World ไม่ถูกต้อง"))
	}
	c.Set("X-Content-Type-Options", "nosniff")
	c.Locals("identity", who)
	if who.Role != RoleAdmin && !s.userMay(c) {
		return c.Status(fiber.StatusForbidden).JSON(failEnv("AIAGENT_ADMIN_ONLY", "ต้องใช้ token ของ admin"))
	}
	if who.DeviceID != "" && !deviceMay(c) {
		return c.Status(fiber.StatusForbidden).JSON(failEnv("AIAGENT_DEVICE_FORBIDDEN", "ทำจากโทรศัพท์ไม่ได้ ใช้หน้าเว็บด้วย token ของ admin"))
	}
	if who.Role != RoleAdmin && !userTextAllowed(c) {
		return c.Status(fiber.StatusForbidden).JSON(failEnv("AIAGENT_COMMAND_BLOCKED", "คำสั่งนี้ใช้ได้เฉพาะ admin"))
	}
	// before a send is handed to a room: the room's instance checks again
	return s.once(c, func() error { return s.route(c, who) })
}

// route sends a request on to the room it belongs to, or to this service's
// own handlers.
func (s *Service) route(c fiber.Ctx, who Identity) error {
	if s.OSUsers != nil {
		target := who.OSUser
		if who.Role == RoleAdmin {
			target = strings.Clone(c.Get(AsHeader))
		} else if target == "" {
			// never let a user without an account reach the admin's own agents
			return c.Status(fiber.StatusForbidden).JSON(failEnv("AIAGENT_ADMIN_ONLY", "token นี้ไม่มีห้อง ให้ขอ token ใหม่จาก admin"))
		}
		local := localPath(strings.TrimPrefix(c.Path(), apiPrefix))
		if target != "" && !local {
			return s.forwardAudited(c, target)
		}
		// an admin acts on a room's session straight from the list: the
		// request goes to the room that session lives in
		if target == "" && !local {
			if m := sessionPathRE.FindStringSubmatch(strings.TrimPrefix(c.Path(), apiPrefix)); m != nil {
				if mate := s.teammateOf(c.Context(), "", m[1]); mate != "" {
					return s.forwardAudited(c, strings.Clone(mate))
				}
			}
			// and answers a review page filed in a room the same way
			if m := reviewPathRE.FindStringSubmatch(strings.TrimPrefix(c.Path(), apiPrefix)); m != nil {
				if room := s.roomOfReview(c.Context(), m[1]); room != "" {
					s.roomRev.Lock()
					s.roomRev.at = time.Time{} // the answer changes the list
					s.roomRev.Unlock()
					return s.forwardAudited(c, strings.Clone(room))
				}
			}
		}
	}
	return c.Next()
}

func (s *Service) pageHeaders(c fiber.Ctx) {
	c.Set("Content-Security-Policy", securityHeader)
	c.Set("X-Content-Type-Options", "nosniff")
	c.Set("Referrer-Policy", "no-referrer")
	c.Set("X-Frame-Options", "DENY")
}

func (s *Service) asset(name, typ string) fiber.Handler {
	return func(c fiber.Ctx) error {
		if !s.Store.Enabled() {
			return hidden(c)
		}
		b, err := fs.ReadFile(uiFS, name)
		if err != nil {
			return hidden(c)
		}
		s.pageHeaders(c)
		c.Set("Cache-Control", "no-cache, no-store, must-revalidate")
		c.Status(fiber.StatusOK).Type(typ)
		return c.Send(b)
	}
}

// Mount registers the page and API. When this service shares a listener with
// another app, call it BEFORE that app's static fallback so /aiagent is never
// answered with the other app's UI.
func (s *Service) Mount(app *fiber.App) {
	s.policyOnce.Do(func() {
		s.StartPolicy(context.Background())
		s.StartOutbox(context.Background())
		s.StartOrchestrator(context.Background())
		s.StartPush(context.Background())
	})
	// The preview host serves rooms' pages on its own listener; nothing of
	// this one is reachable by that name.
	app.Use(func(c fiber.Ctx) error {
		if s.Preview.onHost(string(c.Request().Host())) {
			return c.SendStatus(fiber.StatusNotFound)
		}
		if !s.hostAllowed(string(c.Request().Host())) {
			return hidden(c)
		}
		return c.Next()
	})
	page := s.asset("index.html", "html")
	// On its own hostname — behind a tunnel or a reverse proxy — the /aiagent
	// prefix is redundant, so the root serves the page itself rather than
	// bouncing through a redirect the operator then sees in the address bar.
	// The page's own asset and API URLs stay absolute under /aiagent, so both
	// entry points load exactly the same application.
	//
	// Like every other route it answers 404 while the switch is off: anything
	// else here would announce that something is installed at this address.
	app.Get("/", page)
	app.Get("/aiagent", page)
	app.Get("/aiagent/", page)
	app.Get("/aiagent/i18n.js", s.asset("i18n.js", "js"))
	app.Get("/aiagent/app.js", s.asset("app.js", "js"))
	app.Get("/aiagent/styles.css", s.asset("styles.css", "css"))
	// The manifest is what makes this installable to a home screen. It is not
	// secret, but it still answers 404 while the switch is off like everything
	// else, so a disabled install stays indistinguishable from an absent one.
	app.Get("/aiagent/manifest.webmanifest", func(c fiber.Ctx) error {
		if !s.Store.Enabled() {
			return hidden(c)
		}
		b, err := fs.ReadFile(uiFS, "manifest.webmanifest")
		if err != nil {
			return hidden(c)
		}
		// Set the header directly: Type() treats its argument as a file
		// extension, and "application/manifest+json" is not one, so the
		// manifest went out as application/octet-stream and was ignored.
		s.pageHeaders(c)
		c.Set("Content-Type", "application/manifest+json; charset=utf-8")
		return c.Status(fiber.StatusOK).Send(b)
	})
	app.Get("/aiagent/assets/:name", s.handleAsset)
	app.Get("/aiagent/fonts/:name", s.handleFont)
	app.Get("/aiagent/review-view/:ticket", s.handleReviewView)
	// the phone app trades a one-time pairing code for its token (pair.go)
	app.Post("/aiagent/pair", s.handlePairRedeem)
	// a terminal or VS Code session's own `aiagent channel` (channels_http.go)
	s.mountChannels(app)
	// a browser on the login page asks to be signed in by the app, and
	// polls with its own nonce (loginqr.go)
	app.Post("/aiagent/login-qr", s.handleLoginQRCreate)
	app.Get("/aiagent/login-qr/:id", s.handleLoginQRPoll)

	api := app.Group("/aiagent/api", s.gate)
	api.Get("/me", s.handleMe)
	api.Post("/push/devices", s.handlePushDeviceAdd)
	api.Post("/connect/pair", s.handleConnectPair)
	api.Post("/connect/pair/cancel", s.handleConnectPairCancel)
	api.Get("/connect/devices", s.handleDeviceList)
	api.Post("/connect/devices/:id/revoke", s.handleDeviceRevoke)
	api.Post("/login-qr/:id/info", s.handleLoginQRInfo)
	api.Post("/login-qr/:id/approve", s.handleLoginQRApprove)
	api.Get("/update", s.handleUpdateCheck)
	api.Get("/update/release", s.handleReleaseCheck)
	api.Post("/update", s.handleUpdateApply)
	api.Post("/push/devices/remove", s.handlePushDeviceRemove)
	api.Get("/guides/:name", s.handleGuide)
	api.Get("/rooms", s.handleRooms)
	api.Post("/rooms", s.handleSetRooms)
	api.Get("/vault", s.handleVault)
	api.Post("/vault", s.handleVaultAdd)
	api.Post("/vault/:id/remove", s.handleVaultRemove)
	api.Post("/vault/:id/edit", s.handleVaultEdit)
	api.Get("/tokens", s.handleTokens)
	api.Post("/tokens", s.handleTokenCreate)
	api.Post("/tokens/:id/revoke", s.handleTokenRevoke)
	api.Post("/tokens/:id/reissue", s.handleTokenReissue)
	api.Post("/tokens/:id/duty", s.handleTokenDuty)
	api.Post("/tokens/:id/pair", s.handleTokenPair)
	api.Post("/preview/ticket", s.handlePreviewTicket)
	// a room's own instance; the admin service has no work folder to show
	api.Get("/preview/state", s.handlePreviewState)
	api.Get("/preview/file", s.handlePreviewFile)
	api.Post("/preview/review", s.handlePreviewReview)
	// a room's agent asked for a capture (roomspool.go); only the admin
	// service, through the room's socket, calls these
	api.Get("/preview/capture/wanted", s.handleCaptureWanted)
	api.Post("/preview/capture/result", s.handleCaptureResult)
	api.Get("/snapshot", s.handleSnapshot)
	api.Get("/sessions/:id", s.handleSession)
	api.Get("/runs/:id/log", s.handleRunLog)
	api.Post("/missions", s.handleMission)
	api.Post("/sessions/:id/stop", s.handleStop)
	api.Post("/sessions/:id/resume", s.handleResume)
	api.Get("/sessions/:id/messages", s.handleMessages)
	api.Get("/sessions/:id/suggest", s.handleSuggest)
	api.Post("/uploads", s.handleUpload)
	api.Get("/uploads/:name", s.handleUploadGet)
	api.Post("/sessions/:id/message", s.handleSessionMessage)
	s.mountTasks(api)
	s.mountPM(api)
	api.Post("/sessions/:id/model", s.handleSessionModel)
	api.Post("/sessions/:id/answer", s.handleSessionAnswer)
	api.Post("/sessions/:id/name", s.handleSessionName)
	api.Post("/sessions/:id/look", s.handleSessionLook)
	api.Post("/sessions/:id/admin", s.handleSessionAdmin)
	s.mountFaces(api)
	s.mountEnvironments(api)
	api.Post("/messages", s.handleMessage)
	api.Post("/runs/:id/answer", s.handleAnswer)
	api.Post("/runs/:id/approve", s.handleRunStatus("approve"))
	api.Post("/runs/:id/abort", s.handleRunStatus("abort"))
	api.Get("/accounts", s.handleAccounts)
	api.Post("/accounts/:provider/login", s.handleLogin)
	api.Post("/accounts/:provider/code", s.handleLoginCode)
	api.Post("/accounts/:provider/cancel", s.handleLoginCancel)
	api.Post("/accounts/:provider/logout", s.handleLogout)
	api.Post("/sessions/:id/pending/cancel", s.handleCancelPending)
	api.Get("/reviews", s.handleReviews)
	api.Get("/models", s.handleModels)
	api.Get("/clis", s.handleCLIs)
	api.Post("/clis/update", s.handleCLIUpdate)
	api.Get("/policy", s.handlePolicy)
	api.Post("/policy", s.handleSetPolicy)
	api.Post("/policy/idle", s.handleSetIdle)
	api.Post("/codex/model", s.handleCodexModel)
	api.Post("/reviews/:id/view", s.handleReviewTicket)
	api.Get("/reviews/:id/code", s.handleReviewCode)
	api.Post("/reviews/:id/compose", s.handleReviewCompose)
	api.Post("/reviews/:id/decision", s.handleReviewDecision)
	// anything else under /aiagent stays 404 (never the webinit SPA)
	app.All("/aiagent/*", hidden)
}

// There is deliberately no HTTP route that turns the service on, off, or
// rotates its token. Those three used to sit on a host installer's listener
// behind a first-boot code; here the switch is the local CLI only (`aiagent enable` /
// `disable` / `rotate-token`). The web can make user tokens, admins only, and
// a user token always grants less than the admin token that made it.

var assetName = regexp.MustCompile(`^[A-Za-z0-9-]{1,40}\.(png|jpg)$`)

// fontName is the allowlist for /aiagent/fonts/. Fonts live on their own
// route rather than under assets/ because they need a year-long immutable
// cache and a different content type, and because widening assetName to
// accept woff2 would also widen every scene-image path.
var fontName = regexp.MustCompile(`^[A-Za-z0-9-]{1,40}\.woff2$`)

// handleFont serves one embedded web font. The files are content-addressed by
// weight and subset and never change in place, so they are safe to pin for a
// year; a new font means a new file name.
func (s *Service) handleFont(c fiber.Ctx) error {
	name := c.Params("name")
	if !s.Store.Enabled() || !fontName.MatchString(name) {
		return hidden(c)
	}
	b, err := fs.ReadFile(uiFS, "fonts/"+name)
	if err != nil {
		return hidden(c)
	}
	c.Set("X-Content-Type-Options", "nosniff")
	c.Set("Cache-Control", "private, max-age=31536000, immutable")
	c.Set("Content-Type", "font/woff2")
	return c.Status(fiber.StatusOK).Send(b)
}

// handleAsset serves one embedded scene image (allowlisted names only).
func (s *Service) handleAsset(c fiber.Ctx) error {
	name := c.Params("name")
	if !s.Store.Enabled() || !assetName.MatchString(name) {
		return hidden(c)
	}
	b, err := fs.ReadFile(uiFS, "assets/"+name)
	if err != nil {
		return hidden(c)
	}
	c.Set("X-Content-Type-Options", "nosniff")
	c.Set("Cache-Control", "private, max-age=86400")
	c.Status(fiber.StatusOK).Type(strings.TrimPrefix(filepath.Ext(name), "."))
	return c.Send(b)
}

func (s *Service) handleSnapshot(c fiber.Ctx) error {
	snap := s.Collector.Snapshot(c.Context())
	snap.Sessions = s.applyNames(snap.Sessions)
	snap.nonNil() // applyNames can hand back nil for an empty list
	snap.Pending = s.PendingFor("")
	snap = s.forRole(identity(c), snap)
	if s.OSUsers != nil && identity(c).Role == RoleAdmin {
		// the rooms too, so the admin sees what each user sees
		rooms, _ := s.teammates(c.Context(), "")
		snap.Sessions = append(snap.Sessions, rooms...)
	}
	if identity(c).Role == RoleAdmin {
		s.markRoles(snap.Sessions)
	}
	return c.JSON(okEnv(snap))
}

func (s *Service) handleSession(c fiber.Ctx) error {
	d, err := s.Collector.Detail(c.Context(), c.Params("id"))
	if err != nil {
		return s.fail(c, err)
	}
	return c.JSON(okEnv(d))
}

func (s *Service) handleRunLog(c fiber.Ctx) error {
	lines, err := s.Collector.RunLog(c.Params("id"), 60)
	if err != nil {
		return s.fail(c, err)
	}
	return c.JSON(okEnv(fiber.Map{"lines": lines}))
}

func (s *Service) fail(c fiber.Ctx, err error) error {
	var ae *APIError
	if errors.As(err, &ae) {
		return c.Status(ae.Status).JSON(failEnv(ae.Code, ae.Msg))
	}
	return c.Status(fiber.StatusInternalServerError).JSON(failEnv("AIAGENT_INTERNAL", "เกิดข้อผิดพลาดภายใน"))
}

func paramsHash(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))[:32]
}

// mutate runs one audited, rate-limited write. An audit failure aborts the
// action: nothing reaches an agent unaudited.
func (s *Service) mutate(c fiber.Ctx, rpc string, params []string, fn func() (any, error)) error {
	if !s.allowWrite(identity(c), time.Now()) {
		return c.Status(fiber.StatusTooManyRequests).JSON(failEnv("AIAGENT_SLOW_DOWN", "ทำได้ 1 คำสั่งต่อวินาที รอสักครู่แล้วลองใหม่"))
	}

	sub := subject(c)
	ph := paramsHash(params...)
	if err := s.Auditor.Write(audit.Record{Event: "intent", RPC: rpc, AdminSub: sub, ParamsHash: ph}); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(failEnv("AIAGENT_AUDIT_FAILED", "เขียนบันทึก audit ไม่ได้ จึงไม่ได้ทำคำสั่งนี้"))
	}
	details, err := fn()
	rec := audit.Record{Event: "result", RPC: rpc, AdminSub: sub, ParamsHash: ph, Result: "ok"}
	if err != nil {
		rec.Result, rec.Err = "error", err.Error()
	}
	m, _ := details.(fiber.Map)
	if id, ok := m["runId"].(string); ok && id != "" {
		rec.JobID = id
	}
	_ = s.Auditor.Write(rec)
	if err != nil {
		return s.fail(c, err)
	}
	s.Collector.invalidate()
	return c.JSON(okEnv(details))
}

// userKey names a user token for the per-identity limits; "" is the admin.
// A user's phones share their room's limit.
func userKey(who Identity) string {
	if who.Role == RoleAdmin {
		return ""
	}
	if who.UserID != "" {
		return "id:" + who.UserID
	}
	return "name:" + who.Name
}

// allowWrite spaces each identity's writes writeSpacing apart.
func (s *Service) allowWrite(who Identity, now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := userKey(who)
	if k == "" {
		if now.Sub(s.lastWrite) < writeSpacing {
			return false
		}
		s.lastWrite = now
		return true
	}
	if s.userWrites == nil {
		s.userWrites = map[string]time.Time{}
	}
	if now.Sub(s.userWrites[k]) < writeSpacing {
		return false
	}
	for x, t := range s.userWrites {
		if now.Sub(t) >= writeSpacing {
			delete(s.userWrites, x) // user tokens are few; the table stays small
		}
	}
	s.userWrites[k] = now
	return true
}

// uploadsFor is the upload limiter of one identity.
func (s *Service) uploadsFor(who Identity) *uploadLimiter {
	k := userKey(who)
	if k == "" {
		return &s.uploads
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.userUploads == nil {
		s.userUploads = map[string]*uploadLimiter{}
	}
	l := s.userUploads[k]
	if l == nil {
		l = &uploadLimiter{}
		s.userUploads[k] = l
	}
	return l
}

func (s *Service) handleMission(c fiber.Ctx) error {
	var body struct {
		Workspace string `json:"workspace"`
		Provider  string `json:"provider"`
		Task      string `json:"task"`
	}
	if err := c.Bind().Body(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(failEnv("AIAGENT_BAD_REQUEST", "ข้อมูลที่ส่งมาไม่ใช่ JSON ที่ถูกต้อง"))
	}
	return s.mutate(c, "aiagent.mission", []string{body.Workspace, body.Provider, body.Task}, func() (any, error) {
		runID, ws, err := s.Collector.Dispatch(c.Context(), body.Workspace, body.Provider, body.Task)
		return fiber.Map{"runId": runID, "workspace": ws}, err
	})
}

func (s *Service) handleMessage(c fiber.Ctx) error {
	var body struct {
		Workspace string `json:"workspace"`
		Message   string `json:"message"`
	}
	if err := c.Bind().Body(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(failEnv("AIAGENT_BAD_REQUEST", "ข้อมูลที่ส่งมาไม่ใช่ JSON ที่ถูกต้อง"))
	}
	if w, ok := s.Collector.lookupWorkspace(body.Workspace); ok && w.protected && identity(c).Role != RoleAdmin {
		return c.Status(fiber.StatusForbidden).JSON(failEnv("AIAGENT_ADMIN_ONLY", "ต้องใช้ token ของ admin"))
	}
	return s.mutate(c, "aiagent.message", []string{body.Workspace, body.Message}, func() (any, error) {
		sentAt := s.Collector.now()
		if err := s.Collector.Message(c.Context(), body.Workspace, body.Message); err != nil {
			return fiber.Map{"workspace": body.Workspace}, err
		}
		delivery := DeliveryUnknown
		if id := s.Collector.workspaceSession(c.Context(), body.Workspace); id != "" {
			delivery = s.Collector.AwaitDelivery(c.Context(), id, body.Message, sentAt)
		}
		return fiber.Map{"workspace": body.Workspace, "delivery": delivery}, nil
	})
}

func (s *Service) handleAnswer(c fiber.Ctx) error {
	var body struct {
		Answer string `json:"answer"`
	}
	if err := c.Bind().Body(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(failEnv("AIAGENT_BAD_REQUEST", "ข้อมูลที่ส่งมาไม่ใช่ JSON ที่ถูกต้อง"))
	}
	id := c.Params("id")
	return s.mutate(c, "aiagent.answer", []string{id, body.Answer}, func() (any, error) {
		return fiber.Map{"runId": id}, s.Collector.Answer(c.Context(), id, body.Answer)
	})
}

func (s *Service) handleRunStatus(action string) fiber.Handler {
	return func(c fiber.Ctx) error {
		id := c.Params("id")
		return s.mutate(c, "aiagent."+action, []string{id}, func() (any, error) {
			return fiber.Map{"runId": id}, s.Collector.SetRunStatus(c.Context(), id, action)
		})
	}
}

func (s *Service) handleMessages(c fiber.Ctx) error {
	limit := fiber.Query[int](c, "limit", 60)
	if limit < 1 || limit > 200 {
		limit = 60
	}
	msgs, err := s.Collector.Messages(c.Context(), c.Params("id"), limit)
	if err != nil {
		return s.fail(c, err)
	}
	return c.JSON(okEnv(fiber.Map{"messages": msgs}))
}

func (s *Service) handleUpload(c fiber.Ctx) error {
	if !s.uploadsFor(identity(c)).allow(time.Now()) {
		return c.Status(fiber.StatusTooManyRequests).JSON(failEnv("AIAGENT_SLOW_DOWN", "อัปโหลดถี่เกินไป รอ 1 นาที"))
	}
	body := c.Body()
	sum := sha256.Sum256(body)
	sub := subject(c)
	ph := hex.EncodeToString(sum[:])[:32]
	if err := s.Auditor.Write(audit.Record{Event: "intent", RPC: "aiagent.upload", AdminSub: sub, ParamsHash: ph}); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(failEnv("AIAGENT_AUDIT_FAILED", "เขียนบันทึก audit ไม่ได้ จึงไม่รับไฟล์"))
	}
	p, err := SaveUpload(filepath.Join(s.Store.dir, "uploads"), body, time.Now())
	rec := audit.Record{Event: "result", RPC: "aiagent.upload", AdminSub: sub, ParamsHash: ph, Result: "ok"}
	if err != nil {
		rec.Result, rec.Err = "error", err.Error()
	}
	_ = s.Auditor.Write(rec)
	if err != nil {
		return s.fail(c, err)
	}
	return c.JSON(okEnv(fiber.Map{"path": p}))
}

var uploadName = regexp.MustCompile(`^[0-9]{8}-[0-9]{6}-[0-9a-f]{16}\.(png|jpg|gif|webp)$`)

// handleUploadGet returns one previously uploaded image (token-gated) so the
// page can preview what was sent. A stored zip is never served: the name
// pattern admits pictures only.
func (s *Service) handleUploadGet(c fiber.Ctx) error {
	name := c.Params("name")
	if !uploadName.MatchString(name) {
		return hidden(c)
	}
	b, err := os.ReadFile(filepath.Join(s.Store.dir, "uploads", name))
	if err != nil {
		return hidden(c)
	}
	c.Set("X-Content-Type-Options", "nosniff")
	c.Set("Cache-Control", "private, max-age=3600")
	c.Status(fiber.StatusOK).Type(strings.TrimPrefix(filepath.Ext(name), "."))
	return c.Send(b)
}

func (s *Service) handleSessionModel(c fiber.Ctx) error {
	var body struct {
		Model string `json:"model"`
	}
	if err := c.Bind().Body(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(failEnv("AIAGENT_BAD_REQUEST", "ข้อมูลที่ส่งมาไม่ใช่ JSON ที่ถูกต้อง"))
	}
	id := c.Params("id")
	return s.mutate(c, "aiagent.sessionModel", []string{id, body.Model}, func() (any, error) {
		how, err := s.Collector.SetModel(c.Context(), id, body.Model)
		return fiber.Map{"sessionId": id, "model": body.Model, "via": how}, err
	})
}

func (s *Service) handleSessionMessage(c fiber.Ctx) error {
	var body struct {
		Message string `json:"message"`
	}
	if err := c.Bind().Body(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(failEnv("AIAGENT_BAD_REQUEST", "ข้อมูลที่ส่งมาไม่ใช่ JSON ที่ถูกต้อง"))
	}
	id := c.Params("id")
	return s.mutate(c, "aiagent.sessionMessage", []string{id, body.Message}, func() (any, error) {
		sentAt := s.Collector.now()
		how, err := s.Collector.SendToSession(c.Context(), id, body.Message)
		// An agent that is mid-task cannot be typed into. Rather than lose
		// the message — the picture it refers to is already uploaded — hold
		// it and let the outbox deliver it when the session is free.
		if ae, ok := err.(*APIError); ok && ae.Code == "AIAGENT_RELAY_NOT_READY" {
			p, qerr := s.Enqueue(id, body.Message, time.Now())
			if qerr != nil {
				return nil, qerr
			}
			return fiber.Map{"sessionId": id, "queued": true, "pendingId": p.ID}, nil
		}
		if err != nil {
			return fiber.Map{"sessionId": id, "via": how}, err
		}
		// tmux exiting 0 is not delivery; the transcript is the evidence.
		delivery := s.Collector.AwaitDelivery(c.Context(), id, body.Message, sentAt)
		return fiber.Map{"sessionId": id, "via": how, "delivery": delivery}, nil
	})
}

func (s *Service) handleCancelPending(c fiber.Ctx) error {
	id := c.Params("id")
	return s.mutate(c, "aiagent.pendingCancel", []string{id}, func() (any, error) {
		return fiber.Map{"sessionId": id, "cancelled": s.CancelPending(id, "")}, nil
	})
}

func (s *Service) handleStop(c fiber.Ctx) error {
	id := c.Params("id")
	return s.mutate(c, "aiagent.stop", []string{id}, func() (any, error) {
		how, err := s.Collector.Stop(c.Context(), id)
		return fiber.Map{"sessionId": id, "via": how}, err
	})
}

func (s *Service) handleResume(c fiber.Ctx) error {
	id := c.Params("id")
	return s.mutate(c, "aiagent.resume", []string{id}, func() (any, error) {
		state, err := s.Collector.ResumeSession(c.Context(), id)
		return fiber.Map{"sessionId": id, "state": state}, err
	})
}

// handleSessionName sets or clears an operator-chosen name. It is a write, so
// it goes through mutate() for the audit record and the write rate limit like
// every other write.
func (s *Service) handleSessionName(c fiber.Ctx) error {
	id := c.Params("id")
	var body struct {
		Name string `json:"name"`
	}
	if err := c.Bind().Body(&body); err != nil {
		return s.fail(c, ErrBadRequest(`expected {"name": "..."}`))
	}
	return s.mutate(c, "aiagent.renameSession", []string{id}, func() (any, error) {
		name, err := cleanSessionName(body.Name)
		if err != nil {
			return nil, err
		}

		// Ask the CLI to rename itself first. `/rename` inside the session is
		// the only supported way to change the name Claude Code holds, and it
		// is the only outcome where the two agree: a local alias always
		// disagrees with one side or the other.
		if name != "" {
			if via, err := s.Collector.RenameInCLI(c.Context(), id, name); err == nil {
				// The CLI owns the name now, so no alias is needed — and a
				// leftover one would override the very rename just made.
				if _, err := s.SetSessionName(id, "", ""); err != nil {
					return nil, err
				}
				return fiber.Map{"sessionId": id, "name": name, "via": via, "syncedToCli": true}, nil
			}
		}

		// Not reachable, not Claude Code, or clearing the name: keep a local
		// alias. The CLI's own name goes with it, so a later /rename there
		// takes the name back rather than being ignored.
		over := s.cliName(s.Collector.Snapshot(c.Context()).Sessions, id)
		name, err = s.SetSessionName(id, name, over)
		return fiber.Map{"sessionId": id, "name": name, "via": "local", "syncedToCli": false}, err
	})
}

func (s *Service) handleAccounts(c fiber.Ctx) error {
	return c.JSON(okEnv(fiber.Map{"accounts": s.Collector.Accounts(c.Context())}))
}

func (s *Service) handleLogin(c fiber.Ctx) error {
	var body struct {
		Email string `json:"email"`
	}
	_ = c.Bind().Body(&body)
	p := c.Params("provider")
	return s.mutate(c, "aiagent.accountLogin", []string{p, body.Email}, func() (any, error) {
		f, err := s.Collector.StartLogin(c.Context(), p, strings.TrimSpace(body.Email))
		return fiber.Map{"provider": p, "login": f}, err
	})
}

func (s *Service) handleLoginCode(c fiber.Ctx) error {
	var body struct {
		Code string `json:"code"`
	}
	if err := c.Bind().Body(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(failEnv("AIAGENT_BAD_REQUEST", "ข้อมูลที่ส่งมาไม่ใช่ JSON ที่ถูกต้อง"))
	}
	p := c.Params("provider")
	return s.mutate(c, "aiagent.accountCode", []string{p, body.Code}, func() (any, error) {
		return fiber.Map{"provider": p}, s.Collector.SubmitLoginCode(c.Context(), p, body.Code)
	})
}

func (s *Service) handleLoginCancel(c fiber.Ctx) error {
	p := c.Params("provider")
	return s.mutate(c, "aiagent.accountCancel", []string{p}, func() (any, error) {
		return fiber.Map{"provider": p}, s.Collector.CancelLogin(c.Context(), p)
	})
}

func (s *Service) handleLogout(c fiber.Ctx) error {
	p := c.Params("provider")
	return s.mutate(c, "aiagent.accountLogout", []string{p}, func() (any, error) {
		return fiber.Map{"provider": p}, s.Collector.Logout(c.Context(), p)
	})
}

func (s *Service) handleReviews(c fiber.Ctx) error {
	rs := s.Collector.Reviews()
	if rs == nil {
		rs = []Review{}
	}
	rs = s.reviewsFor(c, rs)
	if identity(c).Role == RoleAdmin {
		more, _ := s.roomReviews(c.Context())
		rs = append(rs, more...)
		sort.SliceStable(rs, func(i, j int) bool { return rs[i].CreatedAt.After(rs[j].CreatedAt) })
	}
	return c.JSON(okEnv(fiber.Map{"reviews": rs}))
}

const reviewViewCSP = "sandbox allow-scripts allow-popups; default-src 'none'; img-src data: blob:; media-src data: blob:; font-src data:; style-src 'unsafe-inline'; script-src 'unsafe-inline'; frame-ancestors 'self'; base-uri 'none'; form-action 'none'"

func (s *Service) handleReviewTicket(c fiber.Ctx) error {
	// Params point into fasthttp's reused buffer; copy before keeping it
	id := strings.Clone(c.Params("id"))
	if _, err := s.Collector.ReviewPage(id); err != nil {
		return s.fail(c, err)
	}
	var rb [24]byte
	_, _ = rand.Read(rb[:])
	t := hex.EncodeToString(rb[:])
	now := time.Now()
	s.mu.Lock()
	if s.tickets == nil {
		s.tickets = map[string]viewTicket{}
	}
	for k, v := range s.tickets {
		if now.After(v.expires) {
			delete(s.tickets, k)
		}
	}
	s.tickets[t] = viewTicket{reviewID: id, expires: now.Add(2 * time.Minute)}
	s.mu.Unlock()
	return c.JSON(okEnv(fiber.Map{"url": "/aiagent/review-view/" + t}))
}

// handleReviewView serves a review page once per ticket, in an opaque-origin
// sandbox (no access to the token, cookies or the API) with no network.
func (s *Service) handleReviewView(c fiber.Ctx) error {
	if !s.Store.Enabled() {
		return hidden(c)
	}
	t := strings.Clone(c.Params("ticket"))
	if done, err := s.userReview(c, t); done {
		return err
	}
	s.mu.Lock()
	v, ok := s.tickets[t]
	delete(s.tickets, t)
	s.mu.Unlock()
	if !ok || time.Now().After(v.expires) {
		return hidden(c)
	}
	page, err := s.Collector.ReviewPage(v.reviewID)
	if err != nil {
		return hidden(c)
	}
	c.Set("Content-Security-Policy", reviewViewCSP)
	c.Set("X-Content-Type-Options", "nosniff")
	c.Set("Referrer-Policy", "no-referrer")
	c.Set("X-Frame-Options", "SAMEORIGIN")
	c.Set("Cache-Control", "no-store")
	c.Status(fiber.StatusOK).Type("html")
	return c.Send(withReviewFrame(page))
}

// handleReviewCode answers the review's code tab: the diff of the commit the
// page describes, read from the room's own checkout.
func (s *Service) handleReviewCode(c fiber.Ctx) error {
	v, err := s.Collector.ReviewCodeDiff(strings.Clone(c.Params("id")))
	if err != nil {
		return s.fail(c, err)
	}
	return c.JSON(okEnv(v))
}

func (s *Service) handleReviewCompose(c fiber.Ctx) error {
	var body struct {
		Items []ReviewItem `json:"items"`
		Note  string       `json:"note"`
	}
	if err := c.Bind().Body(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(failEnv("AIAGENT_BAD_REQUEST", "ข้อมูลที่ส่งมาไม่ใช่ JSON ที่ถูกต้อง"))
	}
	decision, reply, err := s.Collector.ComposeReview(c.Params("id"), body.Items, body.Note)
	if err != nil {
		return s.fail(c, err)
	}
	return c.JSON(okEnv(fiber.Map{"decision": decision, "reply": reply}))
}

func (s *Service) handleReviewDecision(c fiber.Ctx) error {
	var body struct {
		Decision string       `json:"decision"`
		Note     string       `json:"note"`
		Items    []ReviewItem `json:"items"`
		Send     bool         `json:"send"`
		// Resend sends an answered review's unconfirmed reply again; the page
		// sets it only after the operator chose "send again".
		Resend bool `json:"resend"`
	}
	if err := c.Bind().Body(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(failEnv("AIAGENT_BAD_REQUEST", "ข้อมูลที่ส่งมาไม่ใช่ JSON ที่ถูกต้อง"))
	}
	id := c.Params("id")
	return s.mutate(c, "aiagent.reviewDecision", []string{id, body.Decision, body.Note, fmt.Sprint(body.Items), fmt.Sprint(body.Send)}, func() (any, error) {
		var deliver ReviewDeliver
		if body.Send {
			deliver = s.deliverReview
		}
		var reply string
		var sent *ReviewSent
		var err error
		if body.Send && body.Resend {
			reply, sent, err = s.Collector.ResendReview(c.Context(), id, deliver)
		} else {
			reply, sent, err = s.Collector.DecideReview(c.Context(), id, body.Decision, body.Note, body.Items, deliver)
		}
		out := fiber.Map{"reviewId": id, "reply": reply, "via": ""}
		if sent != nil {
			out["via"], out["delivery"], out["queued"] = sent.Via, sent.Delivery, sent.Queued
		}
		return out, err
	})
}

// deliverReview sends a review answer to the review's own session. A busy
// agent gets it from the outbox when it is free, like any web message, and
// a sent one is checked against the session's transcript.
func (s *Service) deliverReview(ctx context.Context, r *Review, reply string) (ReviewSent, error) {
	sentAt := s.Collector.now()
	sid, via, err := s.Collector.SendReview(ctx, r, reply)
	if ae, ok := err.(*APIError); ok && ae.Code == "AIAGENT_RELAY_NOT_READY" && sid != "" {
		if _, qerr := s.enqueue(sid, reply, r.ID, time.Now()); qerr != nil {
			return ReviewSent{}, qerr
		}
		return ReviewSent{Via: "queued", Queued: true}, nil
	}
	if err != nil {
		return ReviewSent{}, err
	}
	return ReviewSent{Via: via, Delivery: s.Collector.AwaitDelivery(ctx, sid, reply, sentAt)}, nil
}

func (s *Service) handleModels(c fiber.Ctx) error {
	type opt struct {
		ID    string `json:"id"`
		Label string `json:"label"`
	}
	var cl []opt
	for _, m := range s.Collector.ClaudeModelMenu() {
		cl = append(cl, opt{m[0], m[1]})
	}
	return c.JSON(okEnv(fiber.Map{"claude": cl, "codex": s.Collector.CodexModels(), "codexDefault": s.Collector.CodexDefaultModel()}))
}

func (s *Service) handleCLIs(c fiber.Ctx) error {
	return c.JSON(okEnv(fiber.Map{"clis": s.Collector.InstalledCLIs(c.Context()), "update": s.Collector.CLIUpdateStatus()}))
}

func (s *Service) handleCLIUpdate(c fiber.Ctx) error {
	return s.mutate(c, "aiagent.cliUpdate", nil, func() (any, error) {
		_, err := s.Collector.StartCLIUpdate()
		return fiber.Map{"update": s.Collector.CLIUpdateStatus()}, err
	})
}

func (s *Service) handlePolicy(c fiber.Ctx) error {
	roots := []string{}
	if s.Collector != nil && s.Collector.TrustedRoots != nil {
		roots = append(roots, s.Collector.TrustedRoots()...)
	}
	// trustedRoots is read-only here: the roots in effect (env or policy),
	// changed only with `aiagent trust-roots` on the machine itself.
	return c.JSON(okEnv(fiber.Map{"policy": s.LoadPolicy(), "trustedRoots": roots}))
}

func (s *Service) handleSetPolicy(c fiber.Ctx) error {
	var body struct {
		Enabled      bool    `json:"enabled"`
		ThresholdPct float64 `json:"thresholdPct"`
		ClaudeModel  string  `json:"claudeModel"`
		CodexModel   string  `json:"codexModel"`
	}
	if err := c.Bind().Body(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(failEnv("AIAGENT_BAD_REQUEST", "ข้อมูลที่ส่งมาไม่ใช่ JSON ที่ถูกต้อง"))
	}
	return s.mutate(c, "aiagent.policy", []string{fmt.Sprint(body.Enabled), fmt.Sprint(body.ThresholdPct), body.ClaudeModel, body.CodexModel}, func() (any, error) {
		p, err := s.SetPolicy(body.Enabled, body.ThresholdPct, body.ClaudeModel, body.CodexModel)
		if err == nil {
			s.Collector.invalidate()
			go s.PolicyTick(context.Background())
		}
		return fiber.Map{"policy": p}, err
	})
}

func (s *Service) handleCodexModel(c fiber.Ctx) error {
	var body struct {
		Model string `json:"model"`
	}
	if err := c.Bind().Body(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(failEnv("AIAGENT_BAD_REQUEST", "ข้อมูลที่ส่งมาไม่ใช่ JSON ที่ถูกต้อง"))
	}
	return s.mutate(c, "aiagent.codexModel", []string{body.Model}, func() (any, error) {
		known := false
		for _, m := range s.Collector.CodexModels() {
			known = known || m.ID == body.Model
		}
		if !known {
			return nil, &APIError{400, "AIAGENT_BAD_MODEL", "ไม่รู้จักโมเดล Codex นี้"}
		}
		prev, err := setCodexModel(filepath.Join(s.Collector.serviceHome(), ".codex", "config.toml"), body.Model)
		return fiber.Map{"model": body.Model, "previous": prev}, err
	})
}

func (s *Service) handleSetIdle(c fiber.Ctx) error {
	var body struct {
		Enabled  bool    `json:"enabled"`
		Minutes  int     `json:"minutes"`
		MaxQuota float64 `json:"maxQuota"`
		Target   string  `json:"target"`
	}
	if err := c.Bind().Body(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(failEnv("AIAGENT_BAD_REQUEST", "ข้อมูลที่ส่งมาไม่ใช่ JSON ที่ถูกต้อง"))
	}
	return s.mutate(c, "aiagent.idlePolicy", []string{fmt.Sprint(body.Enabled), fmt.Sprint(body.Minutes), fmt.Sprint(body.MaxQuota), body.Target}, func() (any, error) {
		p, err := s.SetIdlePolicy(body.Enabled, body.Minutes, body.MaxQuota, body.Target)
		return fiber.Map{"policy": p}, err
	})
}

func (s *Service) handleSuggest(c fiber.Ctx) error {
	v, err := s.Collector.Suggest(c.Context(), c.Params("id"))
	if err != nil {
		return s.fail(c, err)
	}
	return c.JSON(okEnv(fiber.Map{"suggestion": v}))
}
