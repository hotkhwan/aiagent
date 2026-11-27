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
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

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
	// ReadOnly: a menu that cannot be picked from here (a search box, a
	// slider, too many rows); Note says why. Options, if any, are only shown.
	ReadOnly bool   `json:"readOnly,omitempty"`
	Note     string `json:"note,omitempty"`
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

// readPrompt reads the question on a capture-pane -e screen: an
// AskUserQuestion dialog with its tabs and boxes, or any other menu (readMenu).
func readPrompt(screen string) *Prompt {
	plain := sgrRE.ReplaceAllString(screen, "")
	if !onDialog(plain) {
		return nil
	}
	if a := parseAsk(screen); a != nil {
		p := &Prompt{Ask: a, Options: append([]PromptOption(nil), a.opts...), Question: a.Question}
		if a.Review {
			p.Question = "Review your answers"
		}
		return p
	}
	return readMenu(plain)
}

// panePrompt reads the question on the agent pane of a workspace (a tmuxKey)
// that shows the session. A question on its screen (a permission prompt, a
// menu) is invisible in the transcript; without this the web shows an idle
// agent that takes no messages.
func (c *Collector) panePrompt(ctx context.Context, ss *Session, key string) {
	screen, _ := c.tmux(ctx, "capture-pane", "-e", "-p", "-t", tmuxPane(key))
	if !onDialog(sgrRE.ReplaceAllString(screen, "")) {
		return
	}
	ss.Prompt = readPrompt(screen)
	if p := ss.Prompt; p != nil && p.Ask != nil && ss.RuntimeType == "claudeCode" {
		if tp := c.claudeTranscript(ss.home, ss.SessionID); tp != "" {
			if fi, err := os.Stat(tp); err == nil {
				p.Ask.withQuestions(pendingAsk(readTail(tp, fi.Size(), askTailBytes)))
			}
		}
	}
	ss.Status, ss.Title = StatusNeedsInput, "รอคำตอบที่หน้าจอ agent (ถามสิทธิ์หรือเมนู) — admin เปิด tmux ตอบ หรือกด หยุด แล้วส่งใหม่"
}

var (
	promptOptRE = regexp.MustCompile(`^\s*([❯›>])?\s*([1-9])\.\s+(\S.*?)\s*$`)
	// ▔ is the top edge of the box Claude Code 2.1 draws a menu in
	promptRuleRE = regexp.MustCompile(`^[\s─━—▔-]{8,}`)
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
			p.Options = append(p.Options, menuOption(n, m[3], m[1] != ""))
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
		if t != "" && !boxLine(t) {
			q = append([]string{strings.Join(strings.Fields(t), " ")}, q...)
		}
	}
	p.Question = truncateRunes(Redact(strings.Join(q, " ")), 900)
	return p
}

var menuColumnsRE = regexp.MustCompile(`\s{2,}`)

// menuOption is one row: a menu that lays out its rows in columns (/model:
// "Haiku ✔      Haiku 5.5 · Fastest…") gives the first as the label and the
// rest as the detail.
func menuOption(n int, text string, current bool) PromptOption {
	o := PromptOption{N: n, Current: current, NeedsText: promptTextRE.MatchString(text)}
	label, detail := text, ""
	if loc := menuColumnsRE.FindStringIndex(text); loc != nil {
		label, detail = text[:loc[0]], strings.TrimSpace(text[loc[1]:])
	}
	o.Label = truncateRunes(Redact(label), 200)
	if detail != "" {
		o.Detail = truncateRunes(Redact(strings.Join(strings.Fields(detail), " ")), 300)
	}
	return o
}

// boxLine is the frame of a box inside a menu (its search field).
func boxLine(t string) bool {
	return strings.HasPrefix(t, "╭") || strings.HasPrefix(t, "╰") || strings.Contains(t, "⌕")
}

const (
	noteTyping = "เมนูนี้มีช่องค้นหาหรือต้องพิมพ์ เลือกจากเว็บไม่ได้ ให้ตอบในเทอร์มินัล (tmux attach) หรือกด Esc ที่นั่น"
	noteNoList = "เมนูนี้ไม่ใช่รายการให้เลือก (เช่นแถบเลื่อน) เลือกจากเว็บไม่ได้ ให้ตอบในเทอร์มินัล (tmux attach) หรือกด Esc ที่นั่น"
	noteLong   = "เมนูนี้ยาวเกิน 9 ข้อ เลือกจากเว็บไม่ได้ ให้ตอบในเทอร์มินัล (tmux attach)"
)

var (
	menuCurRE    = regexp.MustCompile(`^\s*❯\s+\S`)
	menuFilterRE = regexp.MustCompile(`(?i)type to (filter|search)`)
)

// readMenu reads any menu Claude Code draws over its input box: a numbered
// list (permission prompts, plan approval, /model), else a plain list whose
// highlighted row has the ❯ (/theme). Rows are numbered from 1 in screen
// order; picking one moves the ❯ there with the arrows (menuDriver). A menu
// with a search box, or with no list at all, comes back read-only with a note.
func readMenu(plain string) *Prompt {
	if !onDialog(plain) {
		return nil
	}
	lines := strings.Split(plain, "\n")
	top := -1 // the ▔ edge of the menu's box
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.HasPrefix(strings.TrimSpace(lines[i]), "▔") {
			top = i
			break
		}
	}
	// a numbered list above the box is the conversation, not the menu
	p := parsePrompt(strings.Join(lines[max(top, 0):], "\n"))
	if p == nil {
		p = plainMenu(lines, top)
	}
	if p == nil {
		p = &Prompt{Question: menuTitle(lines, top)}
	}
	switch {
	case menuNeedsTyping(lines, top):
		p.ReadOnly, p.Note = true, noteTyping
	case len(p.Options) == 0:
		p.ReadOnly, p.Note = true, noteNoList
	case len(p.Options) > 9:
		p.ReadOnly, p.Note = true, noteLong
	}
	return p
}

// menuTitle is the first line inside the menu's box.
func menuTitle(lines []string, top int) string {
	if top < 0 {
		return ""
	}
	for _, l := range lines[top+1:] {
		if t := strings.TrimSpace(l); t != "" && !boxLine(t) {
			return truncateRunes(Redact(strings.Join(strings.Fields(t), " ")), 300)
		}
	}
	return ""
}

// menuNeedsTyping: a search box in the menu, or a footer that says to type.
func menuNeedsTyping(lines []string, top int) bool {
	from := top + 1
	if top < 0 {
		from = max(0, len(lines)-40)
	}
	for _, l := range lines[from:] {
		if strings.Contains(l, "⌕") {
			return true
		}
	}
	seen := 0
	for i := len(lines) - 1; i >= 0 && seen < 3; i-- {
		if strings.TrimSpace(lines[i]) == "" {
			continue
		}
		seen++
		if menuFilterRE.MatchString(lines[i]) {
			return true
		}
	}
	return false
}

// plainMenu reads an unnumbered list: the rows next to the ❯ one, up to a
// blank line or a rule. A row indented deeper than the others is the detail
// of the row above it.
func plainMenu(lines []string, top int) *Prompt {
	cur := -1
	for i := len(lines) - 1; i > top; i-- {
		if menuCurRE.MatchString(lines[i]) {
			cur = i
			break
		}
	}
	if cur < 0 {
		return nil
	}
	if top < 0 {
		for i := cur - 1; i >= 0; i-- {
			if promptRuleRE.MatchString(lines[i]) {
				top = i
				break
			}
		}
	}
	row := func(i int) bool {
		t := strings.TrimSpace(lines[i])
		return t != "" && !promptRuleRE.MatchString(lines[i]) && !strings.HasPrefix(t, "╌") && !boxLine(t)
	}
	start, end := cur, cur
	for start-1 > top && row(start-1) {
		start--
	}
	for end+1 < len(lines) && row(end+1) {
		end++
	}
	indent := func(l string) int {
		n := 0
		for _, r := range l {
			if r != ' ' && r != '❯' && r != '✔' {
				break
			}
			n++
		}
		return n
	}
	least := -1
	for i := start; i <= end; i++ {
		if n := indent(lines[i]); least < 0 || n < least {
			least = n
		}
	}
	p := &Prompt{}
	for i := start; i <= end; i++ {
		t := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(lines[i]), "❯"))
		if indent(lines[i]) > least+1 && len(p.Options) > 0 && i != cur {
			if last := &p.Options[len(p.Options)-1]; last.Detail == "" {
				last.Detail = truncateRunes(Redact(t), 300)
			}
			continue
		}
		p.Options = append(p.Options, menuOption(len(p.Options)+1, t, i == cur))
	}
	if len(p.Options) < 2 {
		return nil
	}
	var q []string
	for i := start - 1; i > top && len(q) < 8; i-- {
		if t := strings.TrimSpace(lines[i]); t != "" && !boxLine(t) {
			q = append([]string{strings.Join(strings.Fields(t), " ")}, q...)
		}
	}
	p.Question = truncateRunes(Redact(strings.Join(q, " ")), 900)
	return p
}

// answerTarget is the pane that shows the session's question, locked against
// any other send to it until unlock: the agcmd workspace whose pane shows the
// session, or for a background session the relay pane (relayForAnswer).
func (c *Collector) answerTarget(ctx context.Context, id string) (pane string, unlock func(), err error) {
	c.invalidate()
	var target *Session
	snap := c.Snapshot(ctx)
	for i := range snap.Sessions {
		if snap.Sessions[i].SessionID == id {
			target = &snap.Sessions[i]
		}
	}
	if target == nil || !target.Live {
		return "", nil, ErrConflict("session นี้ไม่มีหน้าต่างที่เว็บเข้าถึงได้")
	}
	if target.DispatchVia != "" {
		w, ok := c.lookupWorkspace(target.DispatchVia)
		if !ok {
			return "", nil, ErrConflict("session นี้ไม่มีหน้าต่างที่เว็บเข้าถึงได้")
		}
		return tmuxPane(w.session), c.lockSend("ws:" + w.session), nil
	}
	if target.MessageVia == "relay" {
		name := relayName(shortID(target.SessionID))
		// the same lock as relaySend: a message and an answer never interleave
		unlock := c.lockSend("relay:" + name)
		pane, err := c.relayForAnswer(ctx, target, name)
		if err != nil {
			unlock()
			return "", nil, err
		}
		return pane, unlock, nil
	}
	return "", nil, ErrConflict("session นี้ไม่มีหน้าต่างที่เว็บเข้าถึงได้")
}

// AnswerPrompt picks choice n of the question the session's pane shows. An
// AskUserQuestion dialog of one question goes through the ask driver
// (ask.go), which sees each key land and submits a multiSelect; any other
// question gets the cursor moved to the choice and Enter.
func (c *Collector) AnswerPrompt(ctx context.Context, id string, n int) error {
	if c.Channels.HasPrompt(id) {
		// a permission prompt the session relayed through its channel:
		// the verdict goes back the same way, no keys are pressed
		defer c.invalidate()
		return c.Channels.Answer(id, n)
	}
	pane, unlock, err := c.answerTarget(ctx, id)
	if err != nil {
		return err
	}
	defer unlock()
	// read it again under the lock: the answer is for what is on screen now
	screen, _ := c.tmux(ctx, "capture-pane", "-e", "-p", "-t", pane)
	if a := parseAsk(screen); a != nil {
		req, err := a.optionAnswer(n)
		if err != nil {
			return err
		}
		defer c.invalidate()
		return c.askDriver(ctx, pane, req).run()
	}
	defer c.invalidate()
	d := &menuDriver{
		read:  func() (string, error) { return c.tmux(ctx, "capture-pane", "-e", "-p", "-t", pane) },
		key:   func(k string) error { _, err := c.tmux(ctx, "send-keys", "-t", pane, k); return err },
		sleep: c.sleep,
	}
	return d.pick(n)
}

// menuDriver picks a row of a menu (readMenu) with the arrows and Enter, the
// keys a person uses, reading the screen before each one: a key is pressed
// only while the screen shows the same menu with the ❯ where it should be.
type menuDriver struct {
	read  func() (string, error) // capture-pane -e -p
	key   func(k string) error   // send-keys <k>
	sleep func(time.Duration)
	// want is the menu the answer is for; nil takes the one first read.
	want *Prompt
	keys int
}

func (d *menuDriver) look() (*Prompt, error) {
	raw, err := d.read()
	if err != nil {
		return nil, &APIError{502, "AIAGENT_SEND_FAILED", "อ่านหน้าจอ agent ไม่ได้"}
	}
	return readMenu(sgrRE.ReplaceAllString(raw, "")), nil
}

func sameMenu(a, b *Prompt) bool {
	if a == nil || b == nil || a.Question != b.Question || len(a.Options) != len(b.Options) {
		return false
	}
	for i := range a.Options {
		if a.Options[i].Label != b.Options[i].Label {
			return false
		}
	}
	return true
}

func menuCursor(p *Prompt) int {
	for _, o := range p.Options {
		if o.Current {
			return o.N
		}
	}
	return 0
}

func (d *menuDriver) pick(n int) error {
	p, err := d.look()
	if err != nil {
		return err
	}
	if p == nil {
		return ErrConflict("agent ไม่ได้แสดงคำถามแล้ว")
	}
	if d.want == nil {
		d.want = p
	} else if !sameMenu(p, d.want) {
		return askStop("เมนูบนหน้าจอไม่ใช่เมนูเดียวกับที่เห็นในเว็บ")
	}
	if p.ReadOnly {
		return ErrConflict(p.Note)
	}
	if n < 1 || n > len(p.Options) {
		return ErrBadRequest("ไม่มีตัวเลือกนี้")
	}
	if p.Options[n-1].NeedsText {
		return ErrConflict("ตัวเลือกนี้ต้องพิมพ์ข้อความ ให้ตอบในเทอร์มินัล")
	}
	for {
		cur := menuCursor(p)
		if cur == 0 {
			return askStop("ไม่เห็นเคอร์เซอร์บนหน้าจอ")
		}
		if cur == n {
			if err := d.key("Enter"); err != nil {
				return &APIError{502, "AIAGENT_SEND_FAILED", "ยืนยันตัวเลือกไม่สำเร็จ"}
			}
			return nil
		}
		k, next := "Down", cur+1
		if cur > n {
			k, next = "Up", cur-1
		}
		if d.keys++; d.keys > askMaxKeys {
			return askStop("กดปุ่มมากเกินกว่าที่ควร")
		}
		if err := d.key(k); err != nil {
			return &APIError{502, "AIAGENT_SEND_FAILED", "เลื่อนไปที่ตัวเลือกไม่สำเร็จ"}
		}
		p = nil
		for waited := time.Duration(0); waited < askWait; waited += askPoll {
			d.sleep(askPoll)
			if s, err := d.look(); err == nil && sameMenu(s, d.want) && menuCursor(s) == next {
				p = s
				break
			}
		}
		if p == nil {
			return askStop("เลื่อนเคอร์เซอร์ไม่สำเร็จ")
		}
	}
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
