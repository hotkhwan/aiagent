// internal/world/http.go
//
// Routes (mounted on this service's own listener, loopback by default):
//
//	GET  /aiagent                      the AI Agent World page (404 while disabled)
//	GET  /aiagent/api/snapshot         token
//	GET  /aiagent/api/sessions/:id     token — session + tool timeline
//	GET  /aiagent/api/runs/:id/log     token — agcmd eng run notes (tail)
//	POST /aiagent/api/missions         token — {workspace, task}   → agcmd eng
//	POST /aiagent/api/messages         token — {workspace, message}→ agcmd dispatch
//	POST /aiagent/api/runs/:id/answer  token — {answer}            → agcmd eng answer
//	POST /aiagent/api/runs/:id/approve token                       → agcmd eng approve
//	POST /aiagent/api/runs/:id/abort   token                       → agcmd eng abort
//	GET  /aiagent/api/sessions/:id/messages token — redacted user/assistant text
//	POST /aiagent/api/sessions/:id/message  token — {message} → that session
//	                                   (agcmd dispatch, or relay for own bg sessions)
//	POST /aiagent/api/uploads          token — raw image body → {path} for a message
//	POST /aiagent/api/sessions/:id/model token — /model <opus|sonnet|haiku> via the session's channel
//	POST /aiagent/api/sessions/:id/stop token — claude stop <id> (own bg session)
//	                                   or agcmd restart <ws> (CLI in a agcmd pane);
//	                                   interactive/foreign sessions are refused
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
	"strings"
	"sync"
	"time"

	"github.com/gofiber/fiber/v3"

	"github.com/hotkhwan/aiagent/internal/audit"
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

	mu        sync.Mutex
	failures  map[string][]time.Time
	lastWrite time.Time
	uploads   uploadLimiter
	tickets   map[string]viewTicket

	policyOnce sync.Once
}

// viewTicket lets one iframe load one review page without the API token.
type viewTicket struct {
	reviewID string
	expires  time.Time
}

// NewService returns a service with packaged defaults.
func NewService(store *Store, col *Collector, auditor *audit.Logger) *Service {
	return &Service{Store: store, Collector: col, Auditor: auditor, failures: map[string][]time.Time{}}
}

const (
	maxFailures    = 10
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
	if len(s.failures) > 4096 {
		s.failures = map[string][]time.Time{ip: s.failures[ip]}
	}
	s.mu.Unlock()
}

// hidden answers 404 so a disabled feature is indistinguishable from absent.
func hidden(c fiber.Ctx) error {
	return c.Status(fiber.StatusNotFound).JSON(failEnv("NOT_FOUND", "not found"))
}

func (s *Service) gate(c fiber.Ctx) error {
	if !s.Store.Enabled() {
		return hidden(c)
	}
	now := time.Now()
	ip := c.IP()
	if s.tooManyFailures(ip, now) {
		return c.Status(fiber.StatusTooManyRequests).JSON(failEnv("AIAGENT_LOCKED", "too many wrong tokens — wait a few minutes"))
	}
	if !s.Store.VerifyToken(c.Get(TokenHeader)) {
		s.recordFailure(ip, now)
		return c.Status(fiber.StatusUnauthorized).JSON(failEnv("AIAGENT_TOKEN_INVALID", "missing or wrong AI Agent World token"))
	}
	c.Set("X-Content-Type-Options", "nosniff")
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
	s.policyOnce.Do(func() { s.StartPolicy(context.Background()) })
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

	api := app.Group("/aiagent/api", s.gate)
	api.Get("/snapshot", s.handleSnapshot)
	api.Get("/sessions/:id", s.handleSession)
	api.Get("/runs/:id/log", s.handleRunLog)
	api.Post("/missions", s.handleMission)
	api.Post("/sessions/:id/stop", s.handleStop)
	api.Get("/sessions/:id/messages", s.handleMessages)
	api.Get("/sessions/:id/suggest", s.handleSuggest)
	api.Post("/uploads", s.handleUpload)
	api.Get("/uploads/:name", s.handleUploadGet)
	api.Post("/sessions/:id/message", s.handleSessionMessage)
	api.Post("/sessions/:id/model", s.handleSessionModel)
	api.Post("/sessions/:id/name", s.handleSessionName)
	api.Post("/messages", s.handleMessage)
	api.Post("/runs/:id/answer", s.handleAnswer)
	api.Post("/runs/:id/approve", s.handleRunStatus("approve"))
	api.Post("/runs/:id/abort", s.handleRunStatus("abort"))
	api.Get("/accounts", s.handleAccounts)
	api.Post("/accounts/:provider/login", s.handleLogin)
	api.Post("/accounts/claude/code", s.handleLoginCode)
	api.Post("/accounts/:provider/cancel", s.handleLoginCancel)
	api.Post("/accounts/:provider/logout", s.handleLogout)
	api.Get("/reviews", s.handleReviews)
	api.Get("/models", s.handleModels)
	api.Get("/policy", s.handlePolicy)
	api.Post("/policy", s.handleSetPolicy)
	api.Post("/policy/idle", s.handleSetIdle)
	api.Post("/codex/model", s.handleCodexModel)
	api.Post("/reviews/:id/view", s.handleReviewTicket)
	api.Post("/reviews/:id/decision", s.handleReviewDecision)
	// anything else under /aiagent stays 404 (never the webinit SPA)
	app.All("/aiagent/*", hidden)
}

// There is deliberately no HTTP route that turns the service on, off, or
// rotates its token. Those three used to sit on a host installer's listener
// behind a first-boot code; here the switch is the local CLI only (`aiagent enable` /
// `disable` / `rotate-token`), so reaching the web surface can never grant
// more than the token already presented.

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
	return c.Status(fiber.StatusInternalServerError).JSON(failEnv("AIAGENT_INTERNAL", "internal error"))
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
	s.mu.Lock()
	if time.Since(s.lastWrite) < writeSpacing {
		s.mu.Unlock()
		return c.Status(fiber.StatusTooManyRequests).JSON(failEnv("AIAGENT_SLOW_DOWN", "one action per second"))
	}
	s.lastWrite = time.Now()
	s.mu.Unlock()

	sub := "aiagent-token@" + c.IP()
	ph := paramsHash(params...)
	if err := s.Auditor.Write(audit.Record{Event: "intent", RPC: rpc, AdminSub: sub, ParamsHash: ph}); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(failEnv("AIAGENT_AUDIT_FAILED", "audit log unavailable — action not performed"))
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

func (s *Service) handleMission(c fiber.Ctx) error {
	var body struct {
		Workspace string `json:"workspace"`
		Task      string `json:"task"`
	}
	if err := c.Bind().Body(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(failEnv("AIAGENT_BAD_REQUEST", "invalid JSON body"))
	}
	return s.mutate(c, "aiagent.mission", []string{body.Workspace, body.Task}, func() (any, error) {
		id, err := s.Collector.Mission(c.Context(), body.Workspace, body.Task)
		return fiber.Map{"runId": id, "workspace": body.Workspace}, err
	})
}

func (s *Service) handleMessage(c fiber.Ctx) error {
	var body struct {
		Workspace string `json:"workspace"`
		Message   string `json:"message"`
	}
	if err := c.Bind().Body(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(failEnv("AIAGENT_BAD_REQUEST", "invalid JSON body"))
	}
	return s.mutate(c, "aiagent.message", []string{body.Workspace, body.Message}, func() (any, error) {
		return fiber.Map{"workspace": body.Workspace}, s.Collector.Message(c.Context(), body.Workspace, body.Message)
	})
}

func (s *Service) handleAnswer(c fiber.Ctx) error {
	var body struct {
		Answer string `json:"answer"`
	}
	if err := c.Bind().Body(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(failEnv("AIAGENT_BAD_REQUEST", "invalid JSON body"))
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
	if !s.uploads.allow(time.Now()) {
		return c.Status(fiber.StatusTooManyRequests).JSON(failEnv("AIAGENT_SLOW_DOWN", "too many uploads — wait a minute"))
	}
	body := c.Body()
	sum := sha256.Sum256(body)
	sub := "aiagent-token@" + c.IP()
	ph := hex.EncodeToString(sum[:])[:32]
	if err := s.Auditor.Write(audit.Record{Event: "intent", RPC: "aiagent.upload", AdminSub: sub, ParamsHash: ph}); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(failEnv("AIAGENT_AUDIT_FAILED", "audit log unavailable — upload refused"))
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
// page can preview what was sent.
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
		return c.Status(fiber.StatusBadRequest).JSON(failEnv("AIAGENT_BAD_REQUEST", "invalid JSON body"))
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
		return c.Status(fiber.StatusBadRequest).JSON(failEnv("AIAGENT_BAD_REQUEST", "invalid JSON body"))
	}
	id := c.Params("id")
	return s.mutate(c, "aiagent.sessionMessage", []string{id, body.Message}, func() (any, error) {
		how, err := s.Collector.SendToSession(c.Context(), id, body.Message)
		return fiber.Map{"sessionId": id, "via": how}, err
	})
}

func (s *Service) handleStop(c fiber.Ctx) error {
	id := c.Params("id")
	return s.mutate(c, "aiagent.stop", []string{id}, func() (any, error) {
		how, err := s.Collector.Stop(c.Context(), id)
		return fiber.Map{"sessionId": id, "via": how}, err
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
		name, err := s.SetSessionName(id, body.Name)
		return fiber.Map{"sessionId": id, "name": name}, err
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
		return c.Status(fiber.StatusBadRequest).JSON(failEnv("AIAGENT_BAD_REQUEST", "invalid JSON body"))
	}
	return s.mutate(c, "aiagent.accountCode", []string{"claude", body.Code}, func() (any, error) {
		return fiber.Map{"provider": "claude"}, s.Collector.SubmitLoginCode(c.Context(), body.Code)
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
	return c.Send(page)
}

func (s *Service) handleReviewDecision(c fiber.Ctx) error {
	var body struct {
		Decision string `json:"decision"`
		Note     string `json:"note"`
		Send     bool   `json:"send"`
	}
	if err := c.Bind().Body(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(failEnv("AIAGENT_BAD_REQUEST", "invalid JSON body"))
	}
	id := c.Params("id")
	return s.mutate(c, "aiagent.reviewDecision", []string{id, body.Decision, body.Note, fmt.Sprint(body.Send)}, func() (any, error) {
		reply, via, err := s.Collector.DecideReview(c.Context(), id, body.Decision, body.Note, body.Send)
		return fiber.Map{"reviewId": id, "reply": reply, "via": via}, err
	})
}

func (s *Service) handleModels(c fiber.Ctx) error {
	type opt struct {
		ID    string `json:"id"`
		Label string `json:"label"`
	}
	var cl []opt
	for _, m := range ClaudeModels {
		cl = append(cl, opt{m[0], m[1]})
	}
	return c.JSON(okEnv(fiber.Map{"claude": cl, "codex": s.Collector.CodexModels(), "codexDefault": s.Collector.CodexDefaultModel()}))
}

func (s *Service) handlePolicy(c fiber.Ctx) error {
	return c.JSON(okEnv(fiber.Map{"policy": s.LoadPolicy()}))
}

func (s *Service) handleSetPolicy(c fiber.Ctx) error {
	var body struct {
		Enabled      bool    `json:"enabled"`
		ThresholdPct float64 `json:"thresholdPct"`
		ClaudeModel  string  `json:"claudeModel"`
		CodexModel   string  `json:"codexModel"`
	}
	if err := c.Bind().Body(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(failEnv("AIAGENT_BAD_REQUEST", "invalid JSON body"))
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
		return c.Status(fiber.StatusBadRequest).JSON(failEnv("AIAGENT_BAD_REQUEST", "invalid JSON body"))
	}
	return s.mutate(c, "aiagent.codexModel", []string{body.Model}, func() (any, error) {
		known := false
		for _, m := range s.Collector.CodexModels() {
			known = known || m.ID == body.Model
		}
		if !known {
			return nil, &APIError{400, "AIAGENT_BAD_MODEL", "unknown Codex model"}
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
		return c.Status(fiber.StatusBadRequest).JSON(failEnv("AIAGENT_BAD_REQUEST", "invalid JSON body"))
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
