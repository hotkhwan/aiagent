// internal/world/channels_http.go
//
// The routes a local `aiagent channel` process calls (channels.go). They are
// not behind the web token: the channel proves itself with the channel key
// (register) and then its session's secret (poll, permission). They answer
// 404 unless channels are on, and to anything that is not a direct loopback
// caller: a channel runs on this machine, never through the tunnel.
//
//	POST /aiagent/channel/register    key    — {sessionId} → {secret}
//	POST /aiagent/channel/poll        secret — {sessionId} → {events}; waits up to ?wait= seconds (max 25)
//	POST /aiagent/channel/permission  secret — {sessionId, requestId, toolName, description, inputPreview}
package world

import (
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
)

// Headers a channel process sends. Neither value is ever logged.
const (
	ChannelKeyHeader    = "X-Aiagent-Channel-Key"
	ChannelSecretHeader = "X-Aiagent-Channel-Secret"
)

func (s *Service) mountChannels(app *fiber.App) {
	app.Post("/aiagent/channel/register", s.channelGate(s.handleChannelRegister))
	app.Post("/aiagent/channel/poll", s.channelGate(s.handleChannelPoll))
	app.Post("/aiagent/channel/permission", s.channelGate(s.handleChannelPermission))
}

// channelGate hides the routes unless channels are on and the caller is a
// direct loopback client, and counts wrong credentials like wrong tokens.
func (s *Service) channelGate(h func(fiber.Ctx, *ChannelHub) error) fiber.Handler {
	return func(c fiber.Ctx) error {
		hub := s.Collector.Channels
		if hub == nil || !s.Store.Enabled() || !directLoopback(c) {
			return hidden(c)
		}
		c.Set("Cache-Control", "no-store")
		now := time.Now()
		ip := clientAddr(c)
		if s.tooManyFailures(ip, now) {
			return c.Status(fiber.StatusTooManyRequests).JSON(failEnv("AIAGENT_LOCKED", "ใส่ token ผิดหลายครั้งเกินไป รอสักครู่แล้วลองใหม่"))
		}
		err := h(c, hub)
		if c.Response().StatusCode() == fiber.StatusUnauthorized {
			s.recordFailure(ip, now)
		}
		return err
	}
}

func directLoopback(c fiber.Ctx) bool {
	return channelPeer(c.IP(), c.Get("Cf-Connecting-Ip"), c.Get("X-Forwarded-For"), c.Get("Forwarded"))
}

// channelPeer decides on plain values; tests swap it because fiber's in-memory
// test connection has no loopback peer.
var channelPeer = channelPeerAllowed

// channelPeerAllowed admits a loopback peer that names no other visitor: the
// tunnel and reverse proxies all arrive from loopback but add their header.
func channelPeerAllowed(peer, cfConnectingIP, forwardedFor, forwarded string) bool {
	if cfConnectingIP != "" || forwardedFor != "" || forwarded != "" {
		return false
	}
	ip := net.ParseIP(peer)
	return ip != nil && ip.IsLoopback()
}

type channelBody struct {
	SessionID string `json:"sessionId"`
	ChannelPermission
}

func (s *Service) handleChannelRegister(c fiber.Ctx, hub *ChannelHub) error {
	var b channelBody
	if err := c.Bind().Body(&b); err != nil {
		return s.fail(c, ErrBadRequest(`expected {"sessionId": "..."}`))
	}
	secret, err := hub.Register(c.Get(ChannelKeyHeader), b.SessionID)
	if err != nil {
		return s.fail(c, err)
	}
	s.Collector.invalidate()
	return c.JSON(okEnv(fiber.Map{"secret": secret, "pollSeconds": int(channelPollWait / time.Second)}))
}

func (s *Service) handleChannelPoll(c fiber.Ctx, hub *ChannelHub) error {
	var b channelBody
	if err := c.Bind().Body(&b); err != nil {
		return s.fail(c, ErrBadRequest(`expected {"sessionId": "..."}`))
	}
	wait := channelPollWait
	if v := c.Query("wait"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return s.fail(c, ErrBadRequest("wait is a number of seconds"))
		}
		if d := time.Duration(n) * time.Second; d < wait {
			wait = d
		}
	}
	events, err := hub.Poll(c.Context(), b.SessionID, c.Get(ChannelSecretHeader), wait)
	if err != nil {
		return s.fail(c, err)
	}
	if events == nil {
		events = []ChannelEvent{}
	}
	return c.JSON(okEnv(fiber.Map{"events": events}))
}

func (s *Service) handleChannelPermission(c fiber.Ctx, hub *ChannelHub) error {
	var b channelBody
	if err := c.Bind().Body(&b); err != nil {
		return s.fail(c, ErrBadRequest("expected a permission request"))
	}
	b.ToolName = strings.TrimSpace(b.ToolName)
	if err := hub.Report(b.SessionID, c.Get(ChannelSecretHeader), b.ChannelPermission); err != nil {
		return s.fail(c, err)
	}
	s.Collector.invalidate()
	return c.JSON(okEnv(fiber.Map{"requestId": b.RequestID}))
}
