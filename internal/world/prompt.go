// internal/world/prompt.go
//
// A question an agent CLI draws over its input box (a permission prompt, a
// choice it asks the user to make) never reaches the transcript, so the web
// could only say "waiting" and refuse to type. This reads the question and its
// numbered choices off the screen and lets the web pick one: the cursor is
// moved to the choice and Enter is pressed, the same keys a person uses. A
// choice that wants typed text is listed but cannot be picked from here.
package world

import (
	"context"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v3"
)

// Prompt is a question on an agent's screen.
type Prompt struct {
	Question string         `json:"question"`
	Options  []PromptOption `json:"options"`
	// Ask is set for Claude Code's AskUserQuestion dialog (ask.go): its
	// tabs, the question on screen and, when the transcript has them, all
	// of its questions. Options then are the question on screen's.
	Ask *AskDialog `json:"ask,omitempty"`
}

// PromptOption is one numbered choice.
type PromptOption struct {
	N       int    `json:"n"`
	Label   string `json:"label"`
	Detail  string `json:"detail,omitempty"`
	Current bool   `json:"current,omitempty"`
	// NeedsText marks a choice that opens a text box.
	NeedsText bool `json:"needsText,omitempty"`
	// Selected: a multiSelect option's box is ticked.
	Selected bool `json:"selected,omitempty"`
}

// readPrompt reads the question on a capture-pane -e screen: parsePrompt, and
// for an AskUserQuestion dialog its tabs and boxes as well.
func readPrompt(screen string) *Prompt {
	plain := sgrRE.ReplaceAllString(screen, "")
	if !onDialog(plain) {
		return nil
	}
	p := parsePrompt(plain)
	if a := parseAsk(screen); a != nil {
		if p == nil {
			p = &Prompt{}
		}
		p.Ask = a
		p.Options = append([]PromptOption(nil), a.opts...)
		p.Question = a.Question
		if a.Review {
			p.Question = "Review your answers"
		}
	}
	return p
}

var (
	promptOptRE  = regexp.MustCompile(`^\s*([❯›>])?\s*([1-9])\.\s+(\S.*?)\s*$`)
	promptRuleRE = regexp.MustCompile(`^[\s─━—-]{8,}`)
	promptTextRE = regexp.MustCompile(`(?i)^(type something|chat about this|other)\b`)
)

// parsePrompt reads the last numbered list on the screen. It returns nil when
// the screen shows no question (onDialog) or no list that starts at 1.
func parsePrompt(screen string) *Prompt {
	if !onDialog(screen) {
		return nil
	}
	lines := strings.Split(screen, "\n")
	start := -1
	for i := len(lines) - 1; i >= 0; i-- {
		if m := promptOptRE.FindStringSubmatch(lines[i]); m != nil && m[2] == "1" {
			start = i
			break
		}
	}
	if start < 0 {
		return nil
	}
	p := &Prompt{}
	for i := start; i < len(lines); i++ {
		l := lines[i]
		if m := promptOptRE.FindStringSubmatch(l); m != nil {
			n, _ := strconv.Atoi(m[2])
			if n != len(p.Options)+1 {
				break
			}
			p.Options = append(p.Options, PromptOption{N: n, Label: truncateRunes(Redact(m[3]), 200), Current: m[1] != "", NeedsText: promptTextRE.MatchString(m[3])})
			continue
		}
		t := strings.TrimSpace(l)
		if t == "" || promptRuleRE.MatchString(l) {
			continue
		}
		if len(p.Options) > 0 && strings.HasPrefix(l, "   ") && p.Options[len(p.Options)-1].Detail == "" {
			p.Options[len(p.Options)-1].Detail = truncateRunes(Redact(t), 300)
		}
	}
	if len(p.Options) < 2 {
		return nil
	}
	var q []string
	for i := start - 1; i >= 0 && len(q) < 8; i-- {
		t := strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(lines[i]), "│|☐☒✔ "))
		if promptRuleRE.MatchString(lines[i]) || strings.HasPrefix(strings.TrimSpace(lines[i]), "❯") {
			break
		}
		if t != "" {
			q = append([]string{t}, q...)
		}
	}
	p.Question = truncateRunes(Redact(strings.Join(q, " ")), 900)
	return p
}

// answerPane is the agcmd workspace whose pane shows the session.
func (c *Collector) answerPane(ctx context.Context, id string) (workspaceEntry, error) {
	c.invalidate()
	var target *Session
	snap := c.Snapshot(ctx)
	for i := range snap.Sessions {
		if snap.Sessions[i].SessionID == id {
			target = &snap.Sessions[i]
		}
	}
	if target == nil || !target.Live || target.DispatchVia == "" {
		return workspaceEntry{}, ErrConflict("session นี้ไม่มีหน้าต่างที่เว็บเข้าถึงได้")
	}
	w, ok := c.lookupWorkspace(target.DispatchVia)
	if !ok {
		return workspaceEntry{}, ErrConflict("session นี้ไม่มีหน้าต่างที่เว็บเข้าถึงได้")
	}
	return w, nil
}

// AnswerPrompt picks choice n of the question the session's pane shows.
func (c *Collector) AnswerPrompt(ctx context.Context, id string, n int) error {
	w, err := c.answerPane(ctx, id)
	if err != nil {
		return err
	}
	defer c.lockSend("ws:" + w.session)()
	// read it again under the lock: the answer is for what is on screen now
	p := parsePrompt(c.paneScreen(ctx, w.session))
	if p == nil {
		return ErrConflict("agent ไม่ได้แสดงคำถามแล้ว")
	}
	cur := 1
	var pick *PromptOption
	for i := range p.Options {
		if p.Options[i].Current {
			cur = p.Options[i].N
		}
		if p.Options[i].N == n {
			pick = &p.Options[i]
		}
	}
	if pick == nil {
		return ErrBadRequest("ไม่มีตัวเลือกนี้")
	}
	if pick.NeedsText {
		return ErrConflict("ตัวเลือกนี้ต้องพิมพ์ข้อความ ให้ตอบในเทอร์มินัล")
	}
	pane := tmuxPane(w.session)
	key := "Down"
	steps := n - cur
	if steps < 0 {
		key, steps = "Up", -steps
	}
	for i := 0; i < steps; i++ {
		if _, err := c.tmux(ctx, "send-keys", "-t", pane, key); err != nil {
			return &APIError{502, "AIAGENT_SEND_FAILED", "เลื่อนไปที่ตัวเลือกไม่สำเร็จ"}
		}
	}
	if _, err := c.tmux(ctx, "send-keys", "-t", pane, "Enter"); err != nil {
		return &APIError{502, "AIAGENT_SEND_FAILED", "ยืนยันตัวเลือกไม่สำเร็จ"}
	}
	c.invalidate()
	return nil
}

// handleSessionAnswer takes {option: n} (one choice, as before) or {ask: {...}}
// (a whole AskUserQuestion answer, ask.go).
func (s *Service) handleSessionAnswer(c fiber.Ctx) error {
	var body struct {
		Option int         `json:"option"`
		Ask    *AskRequest `json:"ask"`
	}
	if err := c.Bind().Body(&body); err != nil {
		return s.fail(c, ErrBadRequest(`expected {"option": 1-9} or {"ask": {...}}`))
	}
	id := strings.Clone(c.Params("id"))
	if body.Ask != nil {
		if body.Option != 0 {
			return s.fail(c, ErrBadRequest(`send either "option" or "ask"`))
		}
		req := *body.Ask
		if err := req.check(); err != nil {
			return s.fail(c, err)
		}
		// only a digest reaches the audit log: the answers may be personal
		b, _ := json.Marshal(req)
		return s.mutate(c, "aiagent.sessionAsk", []string{id, digest(string(b))}, func() (any, error) {
			return fiber.Map{"sessionId": id, "answers": len(req.Answers), "chat": req.Chat}, s.Collector.AnswerAsk(c.Context(), id, req)
		})
	}
	if body.Option < 1 || body.Option > 9 {
		return s.fail(c, ErrBadRequest(`expected {"option": 1-9}`))
	}
	return s.mutate(c, "aiagent.sessionAnswer", []string{id, strconv.Itoa(body.Option)}, func() (any, error) {
		return fiber.Map{"sessionId": id, "option": body.Option}, s.Collector.AnswerPrompt(c.Context(), id, body.Option)
	})
}
