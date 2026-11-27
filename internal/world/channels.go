// internal/world/channels.go
//
// Channels: a Claude Code session opened in a terminal or in VS Code, which no
// relay may type into (relay.go), can still take messages from the web when it
// runs `aiagent channel` as a Claude Code channel (an MCP server Claude Code
// itself spawns and opts in to with --channels or
// --dangerously-load-development-channels; docs/IDE_SESSIONS.md). Nothing is
// typed into anyone's terminal: Claude Code delivers each message as a
// <channel> event, and relays its own permission prompts to the channel.
//
// The hub is in memory and off unless `aiagent serve --channels` made one.
//
//   - Register needs the channel key, a file only this OS user can read
//     (<state-dir>/channel.key, written by serve). It returns a fresh secret
//     bound to one session id; registering again revokes the old secret.
//   - Poll and Report need that session's secret, so a channel only ever reads
//     its own session's messages and only ever raises its own question.
//   - Send (a web message) and Answer (a web pick on the question) come from
//     the web side, behind the usual token gate.
package world

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"regexp"
	"sync"
	"time"
)

// channelLiveFor is how long after its last poll a channel still counts as
// connected. A poll waits at most channelPollWait, so a working channel is
// never quiet for this long.
const (
	channelLiveFor  = 60 * time.Second
	channelPollWait = 25 * time.Second
	channelQueueMax = 50
	channelTextMax  = 600
)

var (
	channelSessionRE = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	// Claude Code's permission request ids: five lowercase letters, no 'l'.
	channelRequestRE = regexp.MustCompile(`^[a-km-z]{5}$`)
)

func errChannelAuth() error {
	return &APIError{401, "AIAGENT_CHANNEL_AUTH", "channel key or secret is not valid"}
}

// ChannelEvent is one thing a channel hands to its Claude Code session.
type ChannelEvent struct {
	Kind      string `json:"kind"` // message | verdict
	Text      string `json:"text,omitempty"`
	RequestID string `json:"requestId,omitempty"`
	Behavior  string `json:"behavior,omitempty"` // allow | deny
}

// ChannelPermission is a permission prompt Claude Code relayed to a channel.
// Its text came from the session and is shown, never executed.
type ChannelPermission struct {
	RequestID    string `json:"requestId"`
	ToolName     string `json:"toolName"`
	Description  string `json:"description"`
	InputPreview string `json:"inputPreview"`
}

type channelSub struct {
	secret [32]byte
	seen   time.Time
	events []ChannelEvent
	perm   *ChannelPermission
	wake   chan struct{}
}

// ChannelHub routes web messages and answers to channel processes.
type ChannelHub struct {
	key  [32]byte
	now  func() time.Time
	mu   sync.Mutex
	subs map[string]*channelSub
}

// NewChannelHub makes a hub that accepts registrations carrying key. An empty
// key accepts none.
func NewChannelHub(key string, now func() time.Time) *ChannelHub {
	if now == nil {
		now = time.Now
	}
	h := &ChannelHub{now: now, subs: map[string]*channelSub{}}
	if key != "" {
		h.key = sha256.Sum256([]byte(key))
	}
	return h
}

func randomHex(n int) (string, error) {
	raw := make([]byte, n)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

func sameSecret(want [32]byte, got string) bool {
	if got == "" {
		return false
	}
	g := sha256.Sum256([]byte(got))
	return subtle.ConstantTimeCompare(want[:], g[:]) == 1
}

// Register binds a new secret to sessionID and returns it.
func (h *ChannelHub) Register(key, sessionID string) (string, error) {
	if h.key == ([32]byte{}) || !sameSecret(h.key, key) {
		return "", errChannelAuth()
	}
	if !channelSessionRE.MatchString(sessionID) {
		return "", ErrBadRequest("session id is not a Claude Code session id")
	}
	secret, err := randomHex(32)
	if err != nil {
		return "", err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	// forget channels that went away long ago, so the map stays small
	for id, s := range h.subs {
		if h.now().Sub(s.seen) > time.Hour {
			delete(h.subs, id)
		}
	}
	sub := h.subs[sessionID]
	if sub == nil {
		sub = &channelSub{wake: make(chan struct{})}
		h.subs[sessionID] = sub
	}
	// a new channel process: whatever the old one had not shown is stale
	sub.secret = sha256.Sum256([]byte(secret))
	sub.seen = h.now()
	sub.perm = nil
	return secret, nil
}

// authed returns sessionID's subscription when secret is its own. Caller holds mu.
func (h *ChannelHub) authed(sessionID, secret string) (*channelSub, error) {
	sub := h.subs[sessionID]
	if sub == nil || !sameSecret(sub.secret, secret) {
		return nil, errChannelAuth()
	}
	return sub, nil
}

// Poll returns the events queued for sessionID, waiting up to wait for one.
func (h *ChannelHub) Poll(ctx context.Context, sessionID, secret string, wait time.Duration) ([]ChannelEvent, error) {
	deadline := time.NewTimer(wait)
	defer deadline.Stop()
	for {
		h.mu.Lock()
		sub, err := h.authed(sessionID, secret)
		if err != nil {
			h.mu.Unlock()
			return nil, err
		}
		sub.seen = h.now()
		if len(sub.events) > 0 || wait <= 0 {
			out := sub.events
			sub.events = nil
			h.mu.Unlock()
			return out, nil
		}
		wake := sub.wake
		h.mu.Unlock()
		select {
		case <-wake:
		case <-deadline.C:
			wait = 0
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// push queues ev and wakes a waiting poll. Caller holds mu.
func (sub *channelSub) push(ev ChannelEvent) error {
	if len(sub.events) >= channelQueueMax {
		return ErrConflict("channel ของ session นี้ยังไม่ได้รับข้อความที่ค้างอยู่ ลองใหม่ภายหลัง")
	}
	sub.events = append(sub.events, ev)
	close(sub.wake)
	sub.wake = make(chan struct{})
	return nil
}

// Report records the permission prompt a channel's session is showing.
func (h *ChannelHub) Report(sessionID, secret string, p ChannelPermission) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	sub, err := h.authed(sessionID, secret)
	if err != nil {
		return err
	}
	if !channelRequestRE.MatchString(p.RequestID) {
		return ErrBadRequest("request id is not one Claude Code issues")
	}
	p.ToolName = clip(p.ToolName, 80)
	p.Description = clip(p.Description, channelTextMax)
	p.InputPreview = clip(p.InputPreview, channelTextMax)
	sub.seen = h.now()
	sub.perm = &p
	return nil
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

func (h *ChannelHub) liveSub(sessionID string) *channelSub {
	sub := h.subs[sessionID]
	if sub == nil || h.now().Sub(sub.seen) > channelLiveFor {
		return nil
	}
	return sub
}

// Live reports a channel that has polled for sessionID recently.
func (h *ChannelHub) Live(sessionID string) bool {
	if h == nil {
		return false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.liveSub(sessionID) != nil
}

// Send queues a web message for sessionID's channel.
func (h *ChannelHub) Send(sessionID, text string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	sub := h.liveSub(sessionID)
	if sub == nil {
		return ErrConflict("session นี้ไม่ได้เชื่อม channel ของ aiagent อยู่")
	}
	return sub.push(ChannelEvent{Kind: "message", Text: text})
}

// Prompt is the permission question sessionID's channel reported, as the
// web's two-choice question, or nil.
func (h *ChannelHub) Prompt(sessionID string) *Prompt {
	if h == nil {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	sub := h.liveSub(sessionID)
	if sub == nil || sub.perm == nil {
		return nil
	}
	p := sub.perm
	q := "Claude ขอใช้ " + p.ToolName
	if p.Description != "" {
		q += ": " + p.Description
	}
	return &Prompt{Question: q, Options: []PromptOption{
		{N: 1, Label: "Yes", Detail: p.InputPreview},
		{N: 2, Label: "No"},
	}}
}

// HasPrompt reports a channel question waiting on sessionID.
func (h *ChannelHub) HasPrompt(sessionID string) bool { return h.Prompt(sessionID) != nil }

// Answer sends the web's pick (1 allow, 2 deny) on sessionID's question.
func (h *ChannelHub) Answer(sessionID string, n int) error {
	if n != 1 && n != 2 {
		return ErrBadRequest("ตอบได้แค่ 1 (Yes) หรือ 2 (No)")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	sub := h.liveSub(sessionID)
	if sub == nil || sub.perm == nil {
		return ErrConflict("agent ไม่ได้แสดงคำถามแล้ว")
	}
	behavior := "allow"
	if n == 2 {
		behavior = "deny"
	}
	if err := sub.push(ChannelEvent{Kind: "verdict", RequestID: sub.perm.RequestID, Behavior: behavior}); err != nil {
		return err
	}
	sub.perm = nil
	return nil
}
