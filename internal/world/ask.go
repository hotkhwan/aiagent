// internal/world/ask.go
//
// Claude Code's AskUserQuestion dialog, read off the screen and answered from
// the web.
//
// One tool call may ask up to a few questions. Claude Code draws them as tabs
// over the input box, one question on screen at a time:
//
//	←  ☐ Color  ☒ Fruit  ✔ Submit  →      tab row; the tab on screen has a
//	                                       background colour (capture-pane -e)
//	Which fruits do you want?
//	❯ 1. [✔] Apple                         multiSelect: a box per option
//	         Apple fruit option
//	  2. [ ] Banana
//	  3. [ ] Type something                free text (absent with previews)
//	     Next                              (Submit on the last question)
//	──────────────
//	  4. Chat about this                   declines the tool, the agent asks
//	Enter to select · Tab/Arrow keys to navigate · Esc to cancel
//
// and a last "Submit" tab that reviews the answers ("● question / → answer")
// over "1. Submit answers / 2. Cancel". A single question with one choice has
// no tab row arrows and no Submit tab: picking an option sends it.
//
// Keys, as Claude Code 2.1 takes them (checked against the real CLI):
//
//	Up/Down        move the cursor (options, Next/Submit, Chat about this)
//	Enter          single choice: pick and go to the next tab (or send when
//	               there is no Submit tab); multiSelect: toggle the box
//	Space          multiSelect: toggle the box — but on the free-text option
//	               it is typed, so it is never pressed there
//	Right/Left     next/previous tab — not while the cursor is on the
//	               free-text option, where they move the caret
//	text           typed into the free-text option when the cursor is on it,
//	               which also ticks it in a multiSelect
//	digits         pick/toggle at once, but on the free-text option they are
//	               typed, and the Chat digit declines the whole dialog; never
//	               used here
//
// Every key below is pressed only after the screen was read again and still
// shows the dialog the page showed (same tabs, same question on the tab), and
// the next key waits until the screen shows what the last one should have
// done. Anything else stops the answer with a Thai error; nothing is guessed.
package world

import (
	"bytes"
	"context"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// AskDialog is an AskUserQuestion dialog on the screen.
type AskDialog struct {
	Tabs []AskTab `json:"tabs"`
	// Active is the tab on screen; len(Tabs) is the Submit tab, -1 unknown.
	Active    int  `json:"active"`
	HasSubmit bool `json:"hasSubmit,omitempty"`
	// Review: the Submit tab is on screen; Unanswered: it warns that some
	// question has no answer; Summary is what it lists.
	Review     bool         `json:"review,omitempty"`
	Unanswered bool         `json:"unanswered,omitempty"`
	Summary    []AskSummary `json:"summary,omitempty"`
	// The question on screen. Its options, with their boxes, are
	// Prompt.Options.
	Header      string `json:"header,omitempty"`
	Question    string `json:"question,omitempty"`
	MultiSelect bool   `json:"multiSelect,omitempty"`
	Preview     bool   `json:"preview,omitempty"`
	// TextOption is the number of the free-text option, 0 when none.
	TextOption int `json:"textOption,omitempty"`
	// TextEmpty: the free-text option still shows its placeholder.
	TextEmpty bool `json:"textEmpty,omitempty"`
	// Chat: "Chat about this" is offered.
	Chat bool `json:"chat,omitempty"`
	// Questions are all the questions of the dialog, from the tool call in
	// the session's transcript, present only when they match the tabs and the
	// question on screen.
	Questions []AskQuestion `json:"questions,omitempty"`
	// Source is "transcript" when no screen was read: the dialog is the tool
	// call as the transcript has it, the first question in front, nothing
	// answered yet (a background session nobody has attached, askPrompt).
	Source string `json:"source,omitempty"`

	cursor string // "opt:N" | "next" | "chat" | ""
	items  []string
	opts   []PromptOption
	labels map[int]string // raw (unredacted) labels, for comparison
}

// AskTab is one question's tab.
type AskTab struct {
	Header   string `json:"header"`
	Answered bool   `json:"answered,omitempty"`
}

// AskSummary is one line pair of the Submit tab.
type AskSummary struct {
	Question string `json:"question"`
	Answer   string `json:"answer"`
}

// AskQuestion is one question of the tool call.
type AskQuestion struct {
	Header      string      `json:"header"`
	Question    string      `json:"question"`
	MultiSelect bool        `json:"multiSelect,omitempty"`
	Options     []AskChoice `json:"options"`
	// FreeText: the dialog adds a "Type something" option (not with previews).
	FreeText bool `json:"freeText,omitempty"`
}

// AskChoice is one option of a question.
type AskChoice struct {
	Label   string `json:"label"`
	Detail  string `json:"detail,omitempty"`
	Preview string `json:"preview,omitempty"`
}

// ---------- reading the screen ----------

// askLine is one screen line without escapes; hl marks each rune drawn with a
// background colour or in reverse video.
type askLine struct {
	text string
	hl   []bool
}

func ansiLines(screen string) []askLine {
	var out []askLine
	bg, rev := false, false
	for _, l := range strings.Split(screen, "\n") {
		var b strings.Builder
		var hl []bool
		emit := func(s string) {
			for _, r := range s {
				if r == '\x1b' {
					continue
				}
				b.WriteRune(r)
				hl = append(hl, bg || rev)
			}
		}
		pos := 0
		for _, m := range sgrRE.FindAllStringSubmatchIndex(l, -1) {
			emit(l[pos:m[0]])
			bg, rev = sgrBackground(l[m[2]:m[3]], bg, rev)
			pos = m[1]
		}
		emit(l[pos:])
		out = append(out, askLine{b.String(), hl})
	}
	return out
}

// sgrBackground follows the background colour and reverse video through one
// SGR sequence.
func sgrBackground(params string, bg, rev bool) (bool, bool) {
	f := strings.Split(params, ";")
	for i := 0; i < len(f); i++ {
		switch p := f[i]; {
		case p == "" || p == "0":
			bg, rev = false, false
		case p == "7":
			rev = true
		case p == "27":
			rev = false
		case p == "49":
			bg = false
		case p == "38" || p == "48" || p == "58":
			if p == "48" {
				bg = true
			}
			if i+1 < len(f) && f[i+1] == "5" {
				i += 2
			} else if i+1 < len(f) && f[i+1] == "2" {
				i += 4
			}
		case len(p) == 2 && p[0] == '4' && p[1] >= '0' && p[1] <= '7',
			len(p) == 3 && p[:2] == "10" && p[2] >= '0' && p[2] <= '7':
			bg = true
		}
	}
	return bg, rev
}

var (
	askTabRowRE  = regexp.MustCompile(`^\s*(←\s+)?[☐☒✔]\s+\S`)
	askOptRE     = regexp.MustCompile(`^\s*(❯)?\s*([1-9][0-9]?)\.\s+(\S.*?)\s*$`)
	askItemRE    = regexp.MustCompile(`^\s*(❯)?\s+(Next|Submit)\s*$`)
	askChatRE    = regexp.MustCompile(`^\s*(❯)?\s*(?:([1-9][0-9]?)\.\s+)?Chat about this\s*$`)
	askBoxRE     = regexp.MustCompile(`^\[( |✔|✓|x|X)\]\s*(.*)$`)
	askRuleRE    = regexp.MustCompile(`^\s*[─━]{8,}`)
	askPreviewRE = regexp.MustCompile(`\s{3,}[┌│└├╭╰]`)
)

const (
	askPlaceholder = "Type something"
	askTailBytes   = 256 << 10
)

// parseAsk reads an AskUserQuestion dialog from a capture-pane -e screen. It
// returns nil when the screen shows none.
func parseAsk(screen string) *AskDialog {
	lines := ansiLines(screen)
	plain := make([]string, len(lines))
	for i, l := range lines {
		plain[i] = l.text
	}
	if !onDialog(strings.Join(plain, "\n")) {
		return nil
	}
	// the tab row sits right under the rule that opens the dialog
	row := -1
	for i := len(lines) - 1; i > 0; i-- {
		if askTabRowRE.MatchString(plain[i]) && askRuleRE.MatchString(plain[i-1]) {
			row = i
			break
		}
	}
	if row < 0 {
		return nil
	}
	d := &AskDialog{Active: -1, labels: map[int]string{}}
	if !d.readTabs(lines[row]) {
		return nil
	}
	i := row + 1
	for i < len(plain) && strings.TrimSpace(plain[i]) == "" {
		i++
	}
	if i < len(plain) && strings.TrimSpace(plain[i]) == "Review your answers" {
		return d.readReview(plain[i+1:])
	}
	var q []string
	for ; i < len(plain); i++ {
		if askOptRE.MatchString(plain[i]) || askRuleRE.MatchString(plain[i]) {
			break
		}
		if t := strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(plain[i]), "│|")); t != "" {
			q = append(q, t)
		}
	}
	if len(q) == 0 || i == len(plain) {
		return nil
	}
	d.Question = truncateRunes(Redact(strings.Join(q, " ")), 900)
	if d.Active >= 0 && d.Active < len(d.Tabs) {
		d.Header = d.Tabs[d.Active].Header
	}
	if !d.readOptions(plain[i:]) {
		return nil
	}
	return d
}

// readTabs reads the tab row. The tab on screen is the one drawn highlighted.
func (d *AskDialog) readTabs(l askLine) bool {
	rs := []rune(l.text)
	type tok struct {
		mark  rune
		label string
		hl    bool
	}
	var toks []tok
	for i := 0; i < len(rs); i++ {
		if rs[i] != '☐' && rs[i] != '☒' && rs[i] != '✔' {
			continue
		}
		j := i + 1
		for j < len(rs) && rs[j] != '☐' && rs[j] != '☒' && rs[j] != '✔' && rs[j] != '→' {
			j++
		}
		t := tok{mark: rs[i], label: strings.TrimSpace(string(rs[i+1 : j]))}
		for k := i; k < j && k < len(l.hl); k++ {
			if l.hl[k] && rs[k] != ' ' {
				t.hl = true
			}
		}
		toks = append(toks, t)
		i = j - 1
	}
	if len(toks) == 0 {
		return false
	}
	if last := toks[len(toks)-1]; last.mark == '✔' && last.label == "Submit" && len(toks) > 1 {
		d.HasSubmit = true
	}
	for k, t := range toks {
		if d.HasSubmit && k == len(toks)-1 {
			if t.hl {
				d.Active = len(d.Tabs)
			}
			break
		}
		if t.label == "" {
			return false
		}
		if t.hl {
			d.Active = len(d.Tabs)
		}
		d.Tabs = append(d.Tabs, AskTab{Header: truncateRunes(Redact(t.label), 100), Answered: t.mark != '☐'})
	}
	if len(toks) == 1 {
		d.Active = 0 // a lone question is the one on screen
	}
	return true
}

func (d *AskDialog) readReview(rest []string) *AskDialog {
	d.Review = true
	d.Active = len(d.Tabs)
	for _, l := range rest {
		t := strings.TrimSpace(l)
		switch {
		case strings.Contains(t, "Some questions are unanswered"):
			d.Unanswered = true
		case strings.HasPrefix(t, "● "):
			d.Summary = append(d.Summary, AskSummary{Question: truncateRunes(Redact(strings.TrimPrefix(t, "● ")), 900)})
		case strings.HasPrefix(t, "→ ") && len(d.Summary) > 0 && d.Summary[len(d.Summary)-1].Answer == "":
			d.Summary[len(d.Summary)-1].Answer = truncateRunes(Redact(strings.TrimPrefix(t, "→ ")), 900)
		}
	}
	if !d.readOptions(rest) || len(d.items) < 2 || d.labels[1] != "Submit answers" {
		return nil
	}
	return d
}

// readOptions reads the options under the question, the Next/Submit item and
// "Chat about this", and where the cursor is.
func (d *AskDialog) readOptions(rest []string) bool {
	var opts []PromptOption
	ruled, nextAt := false, -1
	lastPreRule := -1
	detailOpen := false
	for _, raw := range rest {
		if askRuleRE.MatchString(raw) {
			ruled, detailOpen = true, false
			continue
		}
		l := raw
		if loc := askPreviewRE.FindStringIndex(l); loc != nil {
			l = l[:loc[0]]
			d.Preview = true
		}
		if m := askChatRE.FindStringSubmatch(l); m != nil && ruled {
			d.Chat = true
			d.items = append(d.items, "chat")
			if m[1] != "" {
				d.cursor = "chat"
			}
			detailOpen = false
			continue
		}
		if m := askItemRE.FindStringSubmatch(l); m != nil && len(opts) > 0 && !ruled {
			nextAt = len(opts)
			d.items = append(d.items, "next")
			if m[1] != "" {
				d.cursor = "next"
			}
			detailOpen = false
			continue
		}
		if m := askOptRE.FindStringSubmatch(l); m != nil {
			n := len(opts) + 1
			if m[2] != strconv.Itoa(n) {
				break
			}
			label := m[3]
			o := PromptOption{N: n, Current: m[1] != ""}
			if b := askBoxRE.FindStringSubmatch(label); b != nil {
				d.MultiSelect = true
				o.Selected = b[1] != " "
				label = b[2]
			}
			d.labels[n] = label
			o.Label = truncateRunes(Redact(label), 200)
			opts = append(opts, o)
			d.items = append(d.items, "opt:"+strconv.Itoa(n))
			if o.Current {
				d.cursor = "opt:" + strconv.Itoa(n)
			}
			if !ruled {
				lastPreRule = n
			}
			detailOpen = true
			continue
		}
		t := strings.TrimSpace(l)
		if t == "" || strings.ContainsAny(t[:1], "┌│└├╭╰") {
			detailOpen = false
			continue
		}
		if detailOpen && strings.HasPrefix(l, "     ") && len(opts) > 0 {
			o := &opts[len(opts)-1]
			o.Detail = truncateRunes(Redact(strings.TrimSpace(o.Detail+" "+t)), 300)
			continue
		}
		detailOpen = false
	}
	if len(opts) < 2 {
		return false
	}
	if !d.Review && !d.Preview {
		// Claude Code adds the free-text option last, before Next/Submit or,
		// for a single choice, before the rule over "Chat about this".
		n := lastPreRule
		if nextAt > 0 {
			n = nextAt
		}
		if n > 0 && (d.Chat || nextAt > 0) {
			d.TextOption = n
			opts[n-1].NeedsText = true
			lb := strings.TrimSuffix(d.labels[n], ".")
			d.TextEmpty = lb == askPlaceholder
		}
	}
	d.opts = opts
	return true
}

// ---------- the tool call in the transcript ----------

// pendingAsk is the last AskUserQuestion call in the transcript lines that has
// no result yet.
func pendingAsk(lines [][]byte) []AskQuestion {
	type input struct {
		Questions []struct {
			Question    string `json:"question"`
			Header      string `json:"header"`
			MultiSelect bool   `json:"multiSelect"`
			Options     []struct {
				Label       string `json:"label"`
				Description string `json:"description"`
				Preview     string `json:"preview"`
			} `json:"options"`
		} `json:"questions"`
	}
	var last struct {
		id string
		in input
	}
	for _, raw := range lines {
		if !bytes.Contains(raw, []byte("AskUserQuestion")) && !bytes.Contains(raw, []byte("tool_result")) {
			continue
		}
		var e struct {
			Type    string `json:"type"`
			Message struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(raw, &e) != nil {
			continue
		}
		var parts []struct {
			Type      string          `json:"type"`
			ID        string          `json:"id"`
			Name      string          `json:"name"`
			Input     json.RawMessage `json:"input"`
			ToolUseID string          `json:"tool_use_id"`
		}
		if json.Unmarshal(e.Message.Content, &parts) != nil {
			continue
		}
		for _, p := range parts {
			switch {
			case e.Type == "assistant" && p.Type == "tool_use" && p.Name == "AskUserQuestion":
				var in input
				if json.Unmarshal(p.Input, &in) == nil {
					last.id, last.in = p.ID, in
				}
			case e.Type == "user" && p.Type == "tool_result" && p.ToolUseID != "" && p.ToolUseID == last.id:
				last.id, last.in = "", input{}
			}
		}
	}
	if last.id == "" || len(last.in.Questions) == 0 || len(last.in.Questions) > 10 {
		return nil
	}
	var out []AskQuestion
	for _, q := range last.in.Questions {
		aq := AskQuestion{
			Header:      truncateRunes(Redact(strings.TrimSpace(q.Header)), 100),
			Question:    truncateRunes(Redact(strings.TrimSpace(q.Question)), 900),
			MultiSelect: q.MultiSelect,
			FreeText:    true,
		}
		if len(q.Options) > 20 {
			return nil
		}
		for _, o := range q.Options {
			c := AskChoice{Label: truncateRunes(Redact(o.Label), 200), Detail: truncateRunes(Redact(o.Description), 300)}
			if o.Preview != "" {
				c.Preview = truncateRunes(Redact(o.Preview), 2000)
				aq.FreeText = false
			}
			aq.Options = append(aq.Options, c)
		}
		out = append(out, aq)
	}
	return out
}

// withQuestions attaches the transcript's questions when they are this
// dialog's: same headers in the same order, the question on screen the same.
func (d *AskDialog) withQuestions(qs []AskQuestion) {
	if len(qs) != len(d.Tabs) {
		return
	}
	for i, q := range qs {
		if !sameText(q.Header, d.Tabs[i].Header) {
			return
		}
	}
	if d.Active >= 0 && d.Active < len(qs) {
		q := qs[d.Active]
		if !sameText(q.Question, d.Question) || q.MultiSelect != d.MultiSelect {
			return
		}
		real := len(d.opts)
		if d.TextOption > 0 {
			real--
		}
		if len(q.Options) != real {
			return
		}
	}
	d.Questions = qs
}

// askPrompt is the dialog of a pending tool call as Claude Code draws it when
// it opens: the first question in front, its options numbered as on screen
// (with "Type something" last unless the question has previews), a Submit tab
// when there is more than one question or the first is a multiSelect, and
// nothing answered. It stands in for the screen of a background session that
// no pane shows; answering still reads the real screen first (AnswerAsk).
func askPrompt(qs []AskQuestion) *Prompt {
	if len(qs) == 0 {
		return nil
	}
	q := qs[0]
	d := &AskDialog{
		Active:      0,
		HasSubmit:   len(qs) > 1 || q.MultiSelect,
		Header:      q.Header,
		Question:    q.Question,
		MultiSelect: q.MultiSelect,
		Chat:        true,
		Questions:   qs,
		Source:      "transcript",
		labels:      map[int]string{},
	}
	for _, x := range qs {
		d.Tabs = append(d.Tabs, AskTab{Header: x.Header})
	}
	for i, o := range q.Options {
		d.opts = append(d.opts, PromptOption{N: i + 1, Label: o.Label, Detail: o.Detail})
		if o.Preview != "" {
			d.Preview = true
		}
	}
	if q.FreeText {
		d.TextOption, d.TextEmpty = len(d.opts)+1, true
		d.opts = append(d.opts, PromptOption{N: d.TextOption, Label: askPlaceholder, NeedsText: true})
	}
	return &Prompt{Question: q.Question, Options: append([]PromptOption(nil), d.opts...), Ask: d}
}

// optionAnswer turns {option: n}, the one choice the phone sends, into a whole
// answer to the dialog on screen. Only a dialog of one question can be
// answered that way, and never with the free-text option.
func (d *AskDialog) optionAnswer(n int) (AskRequest, error) {
	if len(d.Tabs) != 1 || d.Review || d.Active != 0 {
		return AskRequest{}, ErrConflict("คำถามนี้มีหลายข้อ ให้ตอบบนหน้าเว็บ")
	}
	o := d.opt(n)
	if o.N == 0 {
		return AskRequest{}, ErrBadRequest("ไม่มีตัวเลือกนี้")
	}
	if o.NeedsText {
		return AskRequest{}, ErrConflict("ตัวเลือกนี้ต้องพิมพ์ข้อความ ให้ตอบบนหน้าเว็บ")
	}
	return AskRequest{
		Tabs:    []string{d.Tabs[0].Header},
		Answers: []AskAnswer{{Tab: 0, Question: d.Question, Options: []string{o.Label}}},
		Submit:  d.HasSubmit,
	}, nil
}

// sameText compares two texts as drawn: white space (where the screen wraps)
// does not count, and a text cut with … matches what it starts.
func sameText(a, b string) bool {
	sq := func(s string) (string, bool) {
		cut := strings.HasSuffix(s, "…")
		return strings.Join(strings.Fields(strings.TrimSuffix(s, "…")), ""), cut
	}
	x, xc := sq(a)
	y, yc := sq(b)
	if x == y {
		return true
	}
	return xc && strings.HasPrefix(y, x) && x != "" || yc && strings.HasPrefix(x, y) && y != ""
}

// ---------- answering ----------

// AskRequest is a whole answer to the dialog, as the page sends it.
type AskRequest struct {
	// Tabs are the question headers the page showed, in order.
	Tabs []string `json:"tabs"`
	// Answers, at most one per tab.
	Answers []AskAnswer `json:"answers"`
	// Submit sends the answers (the Submit tab) once all are in.
	Submit bool `json:"submit"`
	// Chat picks "Chat about this" instead of answering.
	Chat bool `json:"chat"`
}

// AskAnswer answers one question.
type AskAnswer struct {
	Tab      int    `json:"tab"`
	Question string `json:"question"`
	// Options are the labels picked (one for a single choice).
	Options []string `json:"options"`
	// Text goes in the free-text option.
	Text string `json:"text"`
}

const (
	askMaxText = 500
	askPoll    = 120 * time.Millisecond
	askWait    = 3 * time.Second
	askMaxKeys = 160
)

// check refuses a request that cannot be a whole, sensible answer.
func (r *AskRequest) check() error {
	if len(r.Tabs) == 0 || len(r.Tabs) > 10 {
		return ErrBadRequest("คำตอบต้องบอกหัวข้อคำถาม 1-10 ข้อ")
	}
	for _, t := range r.Tabs {
		if strings.TrimSpace(t) == "" || utf8.RuneCountInString(t) > 120 {
			return ErrBadRequest("หัวข้อคำถามไม่ถูกต้อง")
		}
	}
	if r.Chat {
		if len(r.Answers) > 0 || r.Submit {
			return ErrBadRequest("\"คุยเรื่องนี้\" ส่งคำตอบพร้อมกันไม่ได้")
		}
		return nil
	}
	// no answers with submit: confirm what the Submit tab already lists
	if len(r.Answers) == 0 && !r.Submit {
		return ErrBadRequest("ยังไม่ได้เลือกคำตอบ")
	}
	seen := map[int]bool{}
	for i := range r.Answers {
		a := &r.Answers[i]
		if a.Tab < 0 || a.Tab >= len(r.Tabs) || seen[a.Tab] {
			return ErrBadRequest("คำตอบอ้างถึงคำถามที่ไม่มี หรือตอบข้อเดียวกันซ้ำ")
		}
		seen[a.Tab] = true
		if strings.TrimSpace(a.Question) == "" || utf8.RuneCountInString(a.Question) > 1000 || len(a.Options) > 20 {
			return ErrBadRequest("คำตอบไม่ถูกต้อง")
		}
		for _, o := range a.Options {
			if strings.TrimSpace(o) == "" || utf8.RuneCountInString(o) > 300 {
				return ErrBadRequest("ตัวเลือกไม่ถูกต้อง")
			}
		}
		if a.Text != "" {
			t, err := cleanProse(a.Text)
			if err != nil {
				return err
			}
			if strings.ContainsAny(t, "\n\t") || utf8.RuneCountInString(t) > askMaxText {
				return ErrBadRequest("ข้อความที่พิมพ์ตอบต้องเป็นบรรทัดเดียว ไม่เกิน 500 ตัวอักษร")
			}
			a.Text = t
		}
		if len(a.Options) == 0 && a.Text == "" {
			return ErrBadRequest("คำถาม \"" + r.Tabs[a.Tab] + "\" ยังไม่ได้เลือกคำตอบ")
		}
	}
	// answers are entered tab by tab, left to right
	for i := 1; i < len(r.Answers); i++ {
		for j := i; j > 0 && r.Answers[j].Tab < r.Answers[j-1].Tab; j-- {
			r.Answers[j], r.Answers[j-1] = r.Answers[j-1], r.Answers[j]
		}
	}
	return nil
}

// askDriver presses the keys of one answer, reading the screen around each.
type askDriver struct {
	read  func() (string, error) // capture-pane -e -p
	key   func(k string) error   // send-keys <k>
	typ   func(text string) error
	sleep func(time.Duration)
	req   AskRequest
	keys  int
	// Keys records what was pressed, for the tests.
	pressed []string
}

func askStop(msg string) error {
	return &APIError{409, "AIAGENT_ANSWER_STOPPED", msg + " จึงหยุดไว้ ไม่ได้กดอะไรต่อ ให้ดูหน้าจอ agent แล้วตอบใหม่"}
}

func (d *askDriver) look() (*AskDialog, error) {
	raw, err := d.read()
	if err != nil {
		return nil, &APIError{502, "AIAGENT_SEND_FAILED", "อ่านหน้าจอ agent ไม่ได้"}
	}
	return parseAsk(raw), nil
}

// same is the dialog the page showed.
func (d *askDriver) same(s *AskDialog) error {
	if s == nil {
		return askStop("คำถามของ agent ปิดไปแล้วหรือเปลี่ยนไป")
	}
	if len(s.Tabs) != len(d.req.Tabs) {
		return askStop("คำถามบนหน้าจอไม่ใช่ชุดเดียวกับที่เห็นในเว็บ")
	}
	for i := range s.Tabs {
		if !sameText(s.Tabs[i].Header, d.req.Tabs[i]) {
			return askStop("คำถามบนหน้าจอไม่ใช่ชุดเดียวกับที่เห็นในเว็บ")
		}
	}
	return nil
}

// current reads the screen and checks it is still the dialog.
func (d *askDriver) current() (*AskDialog, error) {
	s, err := d.look()
	if err != nil {
		return nil, err
	}
	return s, d.same(s)
}

// press sends one key and waits for the screen to show want.
func (d *askDriver) press(k string, want func(*AskDialog) bool, fail string) (*AskDialog, error) {
	if d.keys++; d.keys > askMaxKeys {
		return nil, askStop("กดปุ่มมากเกินกว่าที่ควร")
	}
	d.pressed = append(d.pressed, k)
	if err := d.key(k); err != nil {
		return nil, &APIError{502, "AIAGENT_SEND_FAILED", "ส่งปุ่มไปที่หน้าจอ agent ไม่สำเร็จ"}
	}
	for waited := time.Duration(0); waited < askWait; waited += askPoll {
		d.sleep(askPoll)
		s, err := d.look()
		if err == nil && want(s) {
			return s, nil
		}
	}
	return nil, askStop(fail)
}

// goTo brings tab t on screen (len(Tabs) is the Submit tab).
func (d *askDriver) goTo(t int) (*AskDialog, error) {
	for {
		s, err := d.current()
		if err != nil {
			return nil, err
		}
		if s.Active == t {
			return s, nil
		}
		if s.Active < 0 {
			return nil, askStop("ไม่รู้ว่าหน้าจอแสดงคำถามข้อไหนอยู่")
		}
		// Right/Left move the caret while the cursor is on the free-text
		// option: step off it first
		if s.TextOption > 0 && s.cursor == "opt:"+strconv.Itoa(s.TextOption) {
			if _, err := d.moveTo(s, "opt:1"); err != nil {
				return nil, err
			}
			continue
		}
		key, to := "Right", s.Active+1
		if s.Active > t {
			key, to = "Left", s.Active-1
		}
		if _, err := d.press(key, func(n *AskDialog) bool { return d.same(n) == nil && n.Active == to }, "เปลี่ยนไปคำถามถัดไปไม่สำเร็จ"); err != nil {
			return nil, err
		}
	}
}

// moveTo moves the cursor to an item ("opt:N", "next", "chat") one step at a
// time, each step seen on screen.
func (d *askDriver) moveTo(s *AskDialog, item string) (*AskDialog, error) {
	tab := s.Active
	for {
		from, to := indexOf(s.items, s.cursor), indexOf(s.items, item)
		if to < 0 {
			return nil, askStop("ไม่พบตัวเลือกนี้บนหน้าจอ")
		}
		if from < 0 {
			return nil, askStop("ไม่เห็นเคอร์เซอร์บนหน้าจอ")
		}
		if from == to {
			return s, nil
		}
		key, next := "Down", s.items[from+1]
		if from > to {
			key, next = "Up", s.items[from-1]
		}
		var err error
		s, err = d.press(key, func(n *AskDialog) bool { return d.same(n) == nil && n.Active == tab && n.cursor == next }, "เลื่อนเคอร์เซอร์ไม่สำเร็จ")
		if err != nil {
			return nil, err
		}
	}
}

func indexOf(items []string, s string) int {
	for i, v := range items {
		if v == s {
			return i
		}
	}
	return -1
}

// option finds a picked label among the question's own options.
func (s *AskDialog) option(label string) (int, error) {
	found := 0
	for _, o := range s.opts {
		if o.N == s.TextOption {
			continue
		}
		if sameText(o.Label, label) || sameText(Redact(s.labels[o.N]), label) {
			if found != 0 {
				return 0, askStop("ตัวเลือก \"" + label + "\" มีมากกว่าหนึ่งข้อ")
			}
			found = o.N
		}
	}
	if found == 0 {
		return 0, askStop("ไม่พบตัวเลือก \"" + label + "\" ในคำถามบนหน้าจอ")
	}
	return found, nil
}

func (s *AskDialog) opt(n int) PromptOption {
	for _, o := range s.opts {
		if o.N == n {
			return o
		}
	}
	return PromptOption{}
}

// typed reports that the free-text option shows text.
func (s *AskDialog) typed(text string) bool {
	if s.TextOption == 0 {
		return false
	}
	o := s.opt(s.TextOption)
	return sameText(Redact(s.labels[s.TextOption]), Redact(text)) || sameText(o.Label+" "+o.Detail, Redact(text))
}

// at brings question t on screen and checks it is the question answered.
func (d *askDriver) at(a AskAnswer) (*AskDialog, error) {
	s, err := d.goTo(a.Tab)
	if err != nil {
		return nil, err
	}
	if !sameText(s.Question, a.Question) {
		return nil, askStop("คำถามข้อ \"" + d.req.Tabs[a.Tab] + "\" บนหน้าจอไม่ตรงกับที่เห็นในเว็บ")
	}
	return s, nil
}

// fillText puts text in the free-text option, the cursor on it.
func (d *askDriver) fillText(s *AskDialog, a AskAnswer) (*AskDialog, error) {
	if s.TextOption == 0 {
		return nil, askStop("คำถามนี้ไม่มีช่องให้พิมพ์คำตอบ")
	}
	item := "opt:" + strconv.Itoa(s.TextOption)
	s, err := d.moveTo(s, item)
	if err != nil {
		return nil, err
	}
	if s.typed(a.Text) {
		return s, nil
	}
	if !s.TextEmpty {
		// clear what is there, one rune at a time, when all of it is seen
		old := s.labels[s.TextOption]
		if strings.HasSuffix(old, "…") || s.opt(s.TextOption).Detail != "" || utf8.RuneCountInString(old) > askMaxText {
			return nil, askStop("ช่องพิมพ์คำตอบมีข้อความเดิมที่ลบให้ไม่ได้")
		}
		for i := utf8.RuneCountInString(old); i > 0; i-- {
			left := string([]rune(old)[:i-1])
			if _, err := d.press("BSpace", func(n *AskDialog) bool {
				if d.same(n) != nil || n.Active != s.Active || n.cursor != item {
					return false
				}
				return i == 1 && n.TextEmpty || i > 1 && n.labels[n.TextOption] == left
			}, "ลบข้อความเดิมในช่องพิมพ์ไม่สำเร็จ"); err != nil {
				return nil, err
			}
		}
	}
	// the text goes in literally, as any message the web sends
	if s, err = d.current(); err != nil {
		return nil, err
	}
	if s.Active != a.Tab || s.cursor != item || !s.TextEmpty {
		return nil, askStop("ช่องพิมพ์คำตอบไม่อยู่ในสภาพที่พร้อมพิมพ์")
	}
	if d.keys++; d.keys > askMaxKeys {
		return nil, askStop("กดปุ่มมากเกินกว่าที่ควร")
	}
	d.pressed = append(d.pressed, "text:"+a.Text)
	if err := d.typ(a.Text); err != nil {
		return nil, &APIError{502, "AIAGENT_SEND_FAILED", "พิมพ์คำตอบไม่สำเร็จ"}
	}
	for waited := time.Duration(0); waited < askWait; waited += askPoll {
		d.sleep(askPoll)
		if n, err := d.look(); err == nil && d.same(n) == nil && n.Active == a.Tab && n.typed(a.Text) {
			return n, nil
		}
	}
	return nil, askStop("ข้อความที่พิมพ์ไม่ปรากฏในช่องคำตอบตามที่ส่ง")
}

// answer enters one question's answer.
func (d *askDriver) answer(a AskAnswer) error {
	s, err := d.at(a)
	if err != nil {
		return err
	}
	if !s.MultiSelect {
		if len(a.Options)+boolInt(a.Text != "") != 1 {
			return ErrBadRequest("คำถาม \"" + d.req.Tabs[a.Tab] + "\" เลือกได้ข้อเดียว")
		}
		var item string
		if a.Text != "" {
			if s, err = d.fillText(s, a); err != nil {
				return err
			}
			item = "opt:" + strconv.Itoa(s.TextOption)
		} else {
			n, err := s.option(a.Options[0])
			if err != nil {
				return err
			}
			item = "opt:" + strconv.Itoa(n)
			if s, err = d.moveTo(s, item); err != nil {
				return err
			}
		}
		// Enter picks: the tab is ticked and the next one comes up, or a
		// dialog with no Submit tab closes
		if s, err = d.current(); err != nil {
			return err
		}
		if s.Active != a.Tab || s.cursor != item || !sameText(s.Question, a.Question) {
			return askStop("หน้าจอเปลี่ยนไประหว่างเลือกคำตอบ")
		}
		lone := !s.HasSubmit
		_, err = d.press("Enter", func(n *AskDialog) bool {
			if lone {
				return n == nil
			}
			return d.same(n) == nil && n.Tabs[a.Tab].Answered && n.Active != a.Tab
		}, "เลือกคำตอบแล้วแต่หน้าจอไม่แสดงว่าตอบข้อนี้แล้ว")
		return err
	}
	// multiSelect: tick exactly the boxes asked for
	want := map[int]bool{}
	for _, l := range a.Options {
		n, err := s.option(l)
		if err != nil {
			return err
		}
		want[n] = true
	}
	for _, o := range s.opts {
		if o.N == s.TextOption {
			continue
		}
		cur := s.opt(o.N)
		if cur.Selected == want[o.N] {
			continue
		}
		item := "opt:" + strconv.Itoa(o.N)
		if s, err = d.moveTo(s, item); err != nil {
			return err
		}
		if s, err = d.current(); err != nil {
			return err
		}
		if s.Active != a.Tab || s.cursor != item || !sameText(s.Question, a.Question) {
			return askStop("หน้าจอเปลี่ยนไประหว่างเลือกคำตอบ")
		}
		w := want[o.N]
		if s, err = d.press("Space", func(n *AskDialog) bool {
			return d.same(n) == nil && n.Active == a.Tab && n.opt(o.N).Selected == w
		}, "ติ๊กตัวเลือก \""+o.Label+"\" ไม่สำเร็จ"); err != nil {
			return err
		}
	}
	if s.TextOption > 0 {
		item := "opt:" + strconv.Itoa(s.TextOption)
		switch {
		case a.Text != "":
			if s, err = d.fillText(s, a); err != nil {
				return err
			}
			if !s.opt(s.TextOption).Selected {
				// typing ticks it; a box left unticked earlier is ticked by Enter
				if s, err = d.press("Enter", func(n *AskDialog) bool {
					return d.same(n) == nil && n.Active == a.Tab && n.opt(n.TextOption).Selected
				}, "ติ๊กช่องพิมพ์คำตอบไม่สำเร็จ"); err != nil {
					return err
				}
			}
		case s.opt(s.TextOption).Selected:
			if s, err = d.moveTo(s, item); err != nil {
				return err
			}
			if s, err = d.press("Enter", func(n *AskDialog) bool {
				return d.same(n) == nil && n.Active == a.Tab && !n.opt(n.TextOption).Selected
			}, "เอาติ๊กช่องพิมพ์คำตอบออกไม่สำเร็จ"); err != nil {
				return err
			}
		}
	} else if a.Text != "" {
		return askStop("คำถามนี้ไม่มีช่องให้พิมพ์คำตอบ")
	}
	if !s.Tabs[a.Tab].Answered {
		return askStop("หน้าจอยังไม่แสดงว่าตอบคำถามข้อ \"" + d.req.Tabs[a.Tab] + "\" แล้ว")
	}
	return nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// expectedAnswer is what the Submit tab should list for an answer.
func expectedAnswer(a AskAnswer) string {
	parts := append([]string{}, a.Options...)
	if a.Text != "" {
		parts = append(parts, a.Text)
	}
	return strings.Join(parts, ", ")
}

// run enters every answer, then submits and sees the dialog close.
func (d *askDriver) run() error {
	if err := d.req.check(); err != nil {
		return err
	}
	if d.req.Chat {
		return d.chat()
	}
	s, err := d.current()
	if err != nil {
		return err
	}
	lone := !s.HasSubmit
	if lone && (len(d.req.Answers) != 1 || s.MultiSelect || s.Review) {
		return askStop("คำถามบนหน้าจอไม่ใช่แบบที่เห็นในเว็บ")
	}
	for _, a := range d.req.Answers {
		if err := d.answer(a); err != nil {
			return err
		}
	}
	if lone {
		return nil // the pick sent it, and the dialog was seen to close
	}
	if !d.req.Submit {
		return nil
	}
	if s, err = d.goTo(len(d.req.Tabs)); err != nil {
		return err
	}
	if !s.Review {
		return askStop("ไม่เห็นหน้าสรุปคำตอบ")
	}
	if s.Unanswered {
		return askStop("ยังมีคำถามที่ยังไม่ได้ตอบ")
	}
	// the summary must say what was asked for, in the multiSelect's own
	// order: compare as sets of comma-separated parts
	for _, a := range d.req.Answers {
		ok := false
		for _, sum := range s.Summary {
			if sameText(sum.Question, a.Question) {
				ok = sameParts(sum.Answer, expectedAnswer(a))
			}
		}
		if !ok {
			return askStop("หน้าสรุปแสดงคำตอบข้อ \"" + d.req.Tabs[a.Tab] + "\" ไม่ตรงกับที่เลือก")
		}
	}
	if s, err = d.moveTo(s, "opt:1"); err != nil {
		return err
	}
	if s, err = d.current(); err != nil {
		return err
	}
	if !s.Review || s.cursor != "opt:1" || s.labels[1] != "Submit answers" {
		return askStop("หน้าจอเปลี่ยนไประหว่างส่งคำตอบ")
	}
	_, err = d.press("Enter", func(n *AskDialog) bool { return n == nil }, "กดส่งคำตอบแล้วแต่คำถามยังไม่ปิด")
	return err
}

func sameParts(a, b string) bool {
	split := func(s string) map[string]int {
		m := map[string]int{}
		for _, p := range strings.Split(s, ",") {
			m[strings.Join(strings.Fields(p), "")]++
		}
		return m
	}
	x, y := split(a), split(b)
	if len(x) != len(y) {
		return sameText(a, b)
	}
	for k, v := range x {
		if y[k] != v {
			return sameText(a, b)
		}
	}
	return true
}

// chat picks "Chat about this" on the question on screen.
func (d *askDriver) chat() error {
	s, err := d.current()
	if err != nil {
		return err
	}
	if s.Review {
		if s, err = d.goTo(len(s.Tabs) - 1); err != nil {
			return err
		}
	}
	if !s.Chat {
		return askStop("คำถามนี้ไม่มีตัวเลือก \"คุยเรื่องนี้\"")
	}
	if s, err = d.moveTo(s, "chat"); err != nil {
		return err
	}
	if s, err = d.current(); err != nil {
		return err
	}
	if s.cursor != "chat" {
		return askStop("หน้าจอเปลี่ยนไประหว่างเลือก \"คุยเรื่องนี้\"")
	}
	_, err = d.press("Enter", func(n *AskDialog) bool { return n == nil }, "เลือก \"คุยเรื่องนี้\" แล้วแต่คำถามยังไม่ปิด")
	return err
}

// AnswerAsk answers the AskUserQuestion dialog on the session's screen.
func (c *Collector) AnswerAsk(ctx context.Context, id string, req AskRequest) error {
	if err := req.check(); err != nil {
		return err
	}
	pane, unlock, err := c.answerTarget(ctx, id)
	if err != nil {
		return err
	}
	defer unlock()
	defer c.invalidate()
	return c.askDriver(ctx, pane, req).run()
}

// askDriver drives the dialog on a tmux pane target.
func (c *Collector) askDriver(ctx context.Context, pane string, req AskRequest) *askDriver {
	return &askDriver{
		read:  func() (string, error) { return c.tmux(ctx, "capture-pane", "-e", "-p", "-t", pane) },
		key:   func(k string) error { _, err := c.tmux(ctx, "send-keys", "-t", pane, k); return err },
		typ:   func(s string) error { _, err := c.tmux(ctx, "send-keys", "-t", pane, "-l", "--", s); return err },
		sleep: c.sleep,
		req:   req,
	}
}

// tmuxPane is the agent window of a workspace's tmux key.
func tmuxPane(key string) string {
	session, window, ok := strings.Cut(key, ":")
	if !ok {
		window = "agent"
	}
	return "=" + session + ":=" + window
}
