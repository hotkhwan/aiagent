package world

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/gofiber/fiber/v3"

	"github.com/hotkhwan/aiagent/internal/audit"
)

// One message, typed once.
//
// A send waits for the agent's transcript to show the message, which takes
// seconds. A Send button that still worked meanwhile let the same long message
// reach an agent twice inside a minute (owner, 2026-11-25). The page now locks
// its composer until the answer, and this is the service's half:
//
//   - The page names each draft with a random sendId and reuses it when the
//     same draft is sent again. A repeat of an id that already went through
//     gets the first answer back instead of being typed again; a repeat that
//     arrives while the first is still going waits for that one's answer.
//   - The same text to the same target again within dupTextWindow is refused
//     with AIAGENT_DUPLICATE_SEND unless the request says resend: true, which
//     the page sets only after the user chose "send anyway".
//
// It runs in the gate, before a request is handed to a room's instance, so the
// admin's service and the room's own instance each keep the rule; the room's
// instance sees the same body and so the same sendId.
//
// Only digests are kept: no text, no token. Both tables are bounded.

const (
	sendIDTTL      = 5 * time.Minute
	dupTextWindow  = 10 * time.Second
	sendOnceMax    = 1024
	sendWaitLimit  = 2 * time.Minute
	dupSendCode    = "AIAGENT_DUPLICATE_SEND"
	dupSendMessage = "ข้อความเดียวกันนี้เพิ่งส่งถึงปลายทางเดียวกันเมื่อไม่กี่วินาทีก่อน จึงไม่ได้ส่งซ้ำ ถ้าตั้งใจส่งอีกครั้งให้กด \"ส่งซ้ำ\""
)

// sendPathRE lists the requests that type something into an agent.
var sendPathRE = regexp.MustCompile(`^/(sessions/[A-Za-z0-9-]+/(message|answer)|messages|missions|runs/[A-Za-z0-9._-]+/answer|pm/messages)$`)

// sendIDRE is what a page may use as a sendId.
var sendIDRE = regexp.MustCompile(`^[A-Za-z0-9-]{8,64}$`)

type sendResult struct {
	done   chan struct{}
	status int
	body   []byte
	ok     bool
	at     time.Time
}

type sendText struct {
	id   string
	busy bool
	at   time.Time
}

type sendOnce struct {
	mu    sync.Mutex
	ids   map[string]*sendResult
	texts map[string]*sendText
}

// sendBody is what the gate reads of a send. Text is whichever field carries
// what is typed. An option answer carries only a number, and two questions in
// a row may well take the same one, so it has no text and is checked by
// sendId only.
type sendBody struct {
	SendID    string `json:"sendId"`
	Resend    bool   `json:"resend"`
	Workspace string `json:"workspace"`
	Provider  string `json:"provider"`
	Message   string `json:"message"`
	Task      string `json:"task"`
	Answer    string `json:"answer"`
	Text      string `json:"text"`
}

func (b sendBody) text() string {
	for _, v := range []string{b.Message, b.Task, b.Answer, b.Text} {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func digest(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// prune drops what has expired and, past the cap, the oldest finished entries.
// Caller holds mu.
func (o *sendOnce) prune(now time.Time) {
	for k, r := range o.ids {
		if !r.at.IsZero() && now.Sub(r.at) > sendIDTTL {
			delete(o.ids, k)
		}
	}
	for k, t := range o.texts {
		if !t.busy && now.Sub(t.at) > dupTextWindow {
			delete(o.texts, k)
		}
	}
	for len(o.ids) >= sendOnceMax {
		old, oldAt := "", now
		for k, r := range o.ids {
			if !r.at.IsZero() && !r.at.After(oldAt) {
				old, oldAt = k, r.at
			}
		}
		if old == "" {
			break // all still running; they are bounded by the write rate
		}
		delete(o.ids, old)
	}
	for len(o.texts) >= sendOnceMax {
		old, oldAt := "", now
		for k, t := range o.texts {
			if !t.busy && !t.at.After(oldAt) {
				old, oldAt = k, t.at
			}
		}
		if old == "" {
			break
		}
		delete(o.texts, old)
	}
}

// sendOnceTarget is who is asking and where the send goes: the room it is
// forwarded to, the path (which names the session or run) and the workspace.
func sendOnceTarget(c fiber.Ctx, b sendBody) (who, target string) {
	id := identity(c)
	room := id.OSUser
	if id.Role == RoleAdmin {
		room = c.Get(AsHeader)
	}
	return id.Role + ":" + id.Name, room + "|" + strings.TrimPrefix(c.Path(), apiPrefix) + "|" + b.Workspace + "|" + b.Provider
}

// once runs next for a send at most once per sendId, and refuses the same
// text to the same target inside dupTextWindow unless resend is set.
func (s *Service) once(c fiber.Ctx, next func() error) error {
	if c.Method() != fiber.MethodPost || !sendPathRE.MatchString(strings.TrimPrefix(c.Path(), apiPrefix)) {
		return next()
	}
	var b sendBody
	if json.Unmarshal(c.Body(), &b) != nil {
		return next() // the handler answers a bad body
	}
	if b.SendID != "" && !sendIDRE.MatchString(b.SendID) {
		return c.Status(fiber.StatusBadRequest).JSON(failEnv("AIAGENT_BAD_REQUEST", "sendId ไม่ถูกต้อง"))
	}
	who, target := sendOnceTarget(c, b)
	idKey := ""
	if b.SendID != "" {
		idKey = digest(who, target, b.SendID)
	}
	textKey := ""
	if t := b.text(); t != "" {
		textKey = digest(who, target, strings.TrimSpace(t))
	}
	o := &s.sends
	now := time.Now()

	o.mu.Lock()
	if o.ids == nil {
		o.ids, o.texts = map[string]*sendResult{}, map[string]*sendText{}
	}
	o.prune(now)
	if r := o.ids[idKey]; idKey != "" && r != nil {
		o.mu.Unlock()
		return s.replay(c, r, target)
	}
	if t := o.texts[textKey]; textKey != "" && t != nil && !b.Resend && (t.id == "" || t.id != b.SendID) &&
		(t.busy || now.Sub(t.at) <= dupTextWindow) {
		o.mu.Unlock()
		s.auditSend(c, "refused", target)
		return c.Status(fiber.StatusConflict).JSON(failEnv(dupSendCode, dupSendMessage))
	}
	var mine *sendResult
	if idKey != "" {
		mine = &sendResult{done: make(chan struct{})}
		o.ids[idKey] = mine
	}
	var prevText *sendText
	if textKey != "" {
		prevText = o.texts[textKey]
		o.texts[textKey] = &sendText{id: b.SendID, busy: true, at: now}
	}
	o.mu.Unlock()

	err := next()
	status := c.Response().StatusCode()
	ok := err == nil && status >= 200 && status < 300

	o.mu.Lock()
	if mine != nil {
		mine.status, mine.ok, mine.at = status, ok, time.Now()
		mine.body = bytes.Clone(c.Response().Body())
		if !ok && o.ids[idKey] == mine {
			delete(o.ids, idKey) // a failed send may be tried again
		}
		close(mine.done)
	}
	if textKey != "" {
		if ok {
			o.texts[textKey] = &sendText{id: b.SendID, at: time.Now()}
		} else if prevText != nil {
			o.texts[textKey] = prevText
		} else {
			delete(o.texts, textKey)
		}
	}
	o.mu.Unlock()
	return err
}

// replay answers a repeated sendId with the first answer, waiting for it when
// the first is still going.
func (s *Service) replay(c fiber.Ctx, r *sendResult, target string) error {
	ctx, cancel := context.WithTimeout(c.Context(), sendWaitLimit)
	defer cancel()
	select {
	case <-r.done:
	case <-ctx.Done():
		return c.Status(fiber.StatusConflict).JSON(failEnv(dupSendCode, "ข้อความนี้กำลังส่งอยู่ รอผลของครั้งแรกก่อน"))
	}
	s.auditSend(c, "replayed", target)
	body := r.body
	if r.ok {
		body = markReplayed(body)
	}
	c.Set("Content-Type", "application/json")
	c.Set("Cache-Control", "no-store")
	return c.Status(r.status).Send(body)
}

// markReplayed adds replayed: true to a success envelope's details so the page
// can say the message was not typed a second time.
func markReplayed(b []byte) []byte {
	var env map[string]json.RawMessage
	if json.Unmarshal(b, &env) != nil {
		return b
	}
	var d map[string]any
	if json.Unmarshal(env["details"], &d) != nil || d == nil {
		d = map[string]any{}
	}
	d["replayed"] = true
	raw, err := json.Marshal(d)
	if err != nil {
		return b
	}
	env["details"] = raw
	out, err := json.Marshal(env)
	if err != nil {
		return b
	}
	return out
}

// auditSend records a refused or replayed send: the target's digest, never
// the text.
func (s *Service) auditSend(c fiber.Ctx, result, target string) {
	if s.Auditor == nil {
		return
	}
	_ = s.Auditor.Write(audit.Record{Event: "result", RPC: "aiagent.duplicateSend", AdminSub: subject(c), ParamsHash: paramsHash(target), Result: result})
}
