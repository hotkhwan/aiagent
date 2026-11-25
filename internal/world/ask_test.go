package world

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "ask", name+".ansi"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func labels(p *Prompt) []string {
	var out []string
	for _, o := range p.Options {
		s := fmt.Sprint(o.N, ".", o.Label)
		if o.Selected {
			s += "[x]"
		}
		if o.NeedsText {
			s += "(text)"
		}
		if o.Current {
			s = "❯" + s
		}
		out = append(out, s)
	}
	return out
}

func tabs(a *AskDialog) string {
	var out []string
	for i, t := range a.Tabs {
		s := t.Header
		if t.Answered {
			s = "☒" + s
		}
		if i == a.Active {
			s = "[" + s + "]"
		}
		out = append(out, s)
	}
	if a.HasSubmit {
		s := "Submit"
		if a.Active == len(a.Tabs) {
			s = "[Submit]"
		}
		out = append(out, s)
	}
	return strings.Join(out, " ")
}

// Screens captured from Claude Code 2.1 (capture-pane -e -p): what the web
// must read off each.
func TestParseAskOnRealScreens(t *testing.T) {
	cases := []struct {
		file, tabs, question string
		opts                 []string
		multi, preview       bool
		text                 int
		cursor               string
	}{
		{"single", "[Size]", "Which size do you want?", []string{"❯1.Small", "2.Large", "3.Type something.(text)"}, false, false, 3, "opt:1"},
		{"single_multi", "[Pets] Submit", "Which pets do you have? (select all that apply)", []string{"❯1.Cat", "2.Dog", "3.Type something(text)"}, true, false, 3, "opt:1"},
		{"tabs_first", "[Color] Fruit Layout Submit", "Which color do you prefer?", []string{"❯1.Red", "2.Blue", "3.Type something.(text)"}, false, false, 3, "opt:1"},
		{"tabs_multi", "☒Color [Fruit] Layout Submit", "Which fruits do you want? (select all that apply)", []string{"❯1.Apple", "2.Banana", "3.Cherry", "4.Type something(text)"}, true, false, 4, "opt:1"},
		{"tabs_multi_toggled", "☒Color [☒Fruit] Layout Submit", "Which fruits do you want? (select all that apply)", []string{"❯1.Apple[x]", "2.Banana", "3.Cherry[x]", "4.Type something(text)"}, true, false, 4, "opt:1"},
		{"tabs_preview", "☒Color ☒Fruit [Layout] Submit", "Which layout do you prefer?", []string{"❯1.A", "2.B"}, false, true, 0, "opt:1"},
		{"tabs_preview_first", "[Layout] Size Submit", "Which layout do you prefer?", []string{"❯1.A", "2.B"}, false, true, 0, "opt:1"},
		{"tabs_freetext", "[Color] Fruit Submit", "Which color do you prefer?", []string{"1.Red", "2.Blue", "❯3.teal ok(text)"}, false, false, 3, "opt:3"},
		{"tabs_multi_text", "Color [☒Fruit] Submit", "Which fruits do you want? (select all that apply)", []string{"1.Apple", "2.Banana", "❯3.kiwi[x](text)"}, true, false, 3, "opt:3"},
		{"tabs_multi_submit_item", "Color [☒Fruit] Submit", "Which fruits do you want? (select all that apply)", []string{"1.Apple[x]", "2.Banana[x]", "3.kiwi[x](text)"}, true, false, 3, "next"},
		{"tabs_chat_cursor", "[Pets] Size Submit", "Which pets do you have? (select all that apply)", []string{"1.Cat", "2.Dog", "3.Type something(text)"}, true, false, 3, "chat"},
	}
	for _, c := range cases {
		p := readPrompt(fixture(t, c.file))
		if p == nil || p.Ask == nil {
			t.Errorf("%s: no dialog read", c.file)
			continue
		}
		a := p.Ask
		if got := tabs(a); got != c.tabs {
			t.Errorf("%s: tabs = %q, want %q", c.file, got, c.tabs)
		}
		if a.Question != c.question || p.Question != c.question {
			t.Errorf("%s: question = %q / %q", c.file, a.Question, p.Question)
		}
		if got := labels(p); !slices.Equal(got, c.opts) {
			t.Errorf("%s: options = %q, want %q", c.file, got, c.opts)
		}
		if a.MultiSelect != c.multi || a.Preview != c.preview || a.TextOption != c.text || a.cursor != c.cursor || !a.Chat || a.Review {
			t.Errorf("%s: multi=%v preview=%v text=%d cursor=%q chat=%v review=%v", c.file, a.MultiSelect, a.Preview, a.TextOption, a.cursor, a.Chat, a.Review)
		}
	}
	// descriptions stay with their option, the preview box stays out
	p := readPrompt(fixture(t, "tabs_first"))
	if p.Options[0].Detail != "Red color option" || p.Ask.Header != "Color" || !p.Ask.TextEmpty {
		t.Errorf("first: %+v %+v", p.Options[0], p.Ask)
	}
	if p := readPrompt(fixture(t, "tabs_preview")); p.Options[0].Detail != "" || strings.ContainsAny(p.Options[0].Label+p.Options[1].Label, "┌│└") {
		t.Errorf("preview leaked into an option: %+v", p.Options)
	}
	if p := readPrompt(fixture(t, "tabs_freetext")); p.Ask.TextEmpty {
		t.Error("typed text read as the placeholder")
	}
}

// The Submit tab prints no key hints; it is still a dialog, and its summary is
// read.
func TestParseAskReview(t *testing.T) {
	p := readPrompt(fixture(t, "tabs_review"))
	if p == nil || p.Ask == nil || !p.Ask.Review || p.Ask.Unanswered {
		t.Fatalf("%+v", p)
	}
	if got := tabs(p.Ask); got != "☒Color ☒Fruit ☒Layout [Submit]" {
		t.Errorf("tabs = %q", got)
	}
	want := []AskSummary{{"Which color do you prefer?", "Blue"}, {"Which fruits do you want? (select all that apply)", "Apple, Cherry"}, {"Which layout do you prefer?", "B"}}
	if !slices.Equal(p.Ask.Summary, want) {
		t.Errorf("summary = %+v", p.Ask.Summary)
	}
	if got := labels(p); !slices.Equal(got, []string{"❯1.Submit answers", "2.Cancel"}) {
		t.Errorf("options = %q", got)
	}
	u := readPrompt(fixture(t, "tabs_review_unanswered"))
	if u == nil || !u.Ask.Unanswered || len(u.Ask.Summary) != 1 || u.Ask.Summary[0].Answer != "Apple, Banana, kiwi" {
		t.Errorf("unanswered = %+v", u)
	}
	if s := readPrompt(fixture(t, "single_multi_review")); s == nil || tabs(s.Ask) != "☒Pets [Submit]" {
		t.Errorf("single review = %+v", s)
	}
	// without its last lines the review is not taken for a dialog
	if onDialog("❯ 1. Submit answers was the plan\n\n───\n❯ \n? for shortcuts") {
		t.Error("a sentence in the conversation counted as the review")
	}
}

func TestParseAskIgnoresOtherScreens(t *testing.T) {
	plain := sgrRE.ReplaceAllString(fixture(t, "tabs_first"), "")
	if a := parseAsk(strings.Replace(plain, "Enter to select · Tab/Arrow keys to navigate · Esc to cancel", "", 1)); a != nil {
		t.Errorf("no key hints, still read: %+v", a)
	}
	perm := "────────────\n Bash command\n  ls /x\nDo you want to proceed?\n❯ 1. Yes\n  2. No\n\nEsc to cancel · Tab to amend"
	if p := readPrompt(perm); p == nil || p.Ask != nil || len(p.Options) != 2 {
		t.Errorf("permission prompt = %+v", p)
	}
	// a todo list in the conversation is not a tab row
	todo := "────────────\nsome text\n ☐ write tests\n ☐ ship\n\n❯ 1. Yes\n  2. No\nEsc to cancel"
	if p := readPrompt(todo); p == nil || p.Ask != nil {
		t.Errorf("todo list = %+v", p)
	}
}

// ---------- the tool call ----------

const askToolUse = `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_A","name":"AskUserQuestion","input":{"questions":[` +
	`{"question":"Which color do you prefer?","header":"Color","multiSelect":false,"options":[{"label":"Red","description":"Red color option"},{"label":"Blue","description":"Blue color option"}]},` +
	`{"question":"Which fruits do you want? (select all that apply)","header":"Fruit","multiSelect":true,"options":[{"label":"Apple","description":"Apple fruit option"},{"label":"Banana","description":"Banana fruit option"},{"label":"Cherry","description":"Cherry fruit option"}]},` +
	`{"question":"Which layout do you prefer?","header":"Layout","multiSelect":false,"options":[{"label":"A","description":"Layout A","preview":"+--+\n|A |\n+--+"},{"label":"B","description":"Layout B","preview":"+--+\n| B|\n+--+"}]}]}}]}}`

func TestPendingAskFromTheTranscript(t *testing.T) {
	lines := [][]byte{[]byte(`{"type":"user","message":{"content":"ask me"}}`), []byte(askToolUse)}
	qs := pendingAsk(lines)
	if len(qs) != 3 || qs[1].Header != "Fruit" || !qs[1].MultiSelect || len(qs[1].Options) != 3 || !qs[0].FreeText || qs[2].FreeText || qs[2].Options[1].Preview == "" {
		t.Fatalf("%+v", qs)
	}
	// answered: nothing pending
	done := append(lines, []byte(`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_A","content":"ok"}]}}`))
	if qs := pendingAsk(done); qs != nil {
		t.Errorf("answered call still pending: %+v", qs)
	}
	// another call's result does not close it
	other := append(lines, []byte(`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_B","content":"ok"}]}}`))
	if qs := pendingAsk(other); len(qs) != 3 {
		t.Errorf("closed by another call's result")
	}

	// attached only to its own dialog
	p := readPrompt(fixture(t, "tabs_first"))
	p.Ask.withQuestions(qs)
	if len(p.Ask.Questions) != 3 {
		t.Errorf("the matching call was not attached")
	}
	p = readPrompt(fixture(t, "tabs_freetext")) // Color, Fruit: another dialog
	p.Ask.withQuestions(qs)
	if p.Ask.Questions != nil {
		t.Errorf("questions of another dialog attached")
	}
	p = readPrompt(fixture(t, "tabs_multi"))
	changed := pendingAsk([][]byte{[]byte(strings.Replace(askToolUse, "Which fruits do you want?", "Which nuts?", 1))})
	p.Ask.withQuestions(changed)
	if p.Ask.Questions != nil {
		t.Errorf("a call whose question differs from the screen was attached")
	}
}

// ---------- a Claude Code dialog to drive ----------

// simAsk draws an AskUserQuestion dialog the way Claude Code 2.1 does and
// takes keys the way it does (checked against the real CLI, see ask.go).
type simAsk struct {
	qs      []simQ
	active  int
	cursor  int
	closed  string // "submitted" | "chat" | "cancel"
	keys    []string
	drop    map[int]bool // key numbers the CLI does not take
	dropTyp bool
	// onKey runs before key number n is handled
	onKey func(n int, s *simAsk)
}

type simQ struct {
	header, question string
	multi            bool
	opts             []string
	freeText         bool
	pick             int // single: option picked, 1-based; -1 = the text
	checked          []bool
	text             string
	textChecked      bool
}

func newSim(qs ...simQ) *simAsk {
	s := &simAsk{qs: qs}
	for i := range s.qs {
		s.qs[i].checked = make([]bool, len(s.qs[i].opts))
	}
	return s
}

func (s *simAsk) lone() bool { return len(s.qs) == 1 && !s.qs[0].multi }

// items on the current tab: options (+ text), Next/Submit, Chat
func (s *simAsk) items() []string {
	if s.active == len(s.qs) {
		return []string{"submit", "cancel"}
	}
	q := s.qs[s.active]
	var out []string
	for i := range q.opts {
		out = append(out, "opt:"+fmt.Sprint(i+1))
	}
	if q.freeText {
		out = append(out, "text")
	}
	if q.multi {
		out = append(out, "next")
	}
	return append(out, "chat")
}

func (s *simAsk) answered(i int) bool {
	q := s.qs[i]
	if !q.multi {
		return q.pick != 0
	}
	return slices.Contains(q.checked, true) || q.textChecked
}

func (s *simAsk) answer(i int) string {
	q := s.qs[i]
	if !q.multi {
		if q.pick == -1 {
			return q.text
		}
		return q.opts[q.pick-1]
	}
	var p []string
	for k, c := range q.checked {
		if c {
			p = append(p, q.opts[k])
		}
	}
	if q.textChecked {
		p = append(p, q.text)
	}
	return strings.Join(p, ", ")
}

const hl, unhl = "\x1b[38;5;16m\x1b[48;5;153m", "\x1b[39m\x1b[49m"

func (s *simAsk) screen() string {
	var b strings.Builder
	b.WriteString("● earlier work\n\n")
	if s.closed != "" {
		b.WriteString("────────────────────────────────────────\n❯ \n────────────────────────────────────────\n  ? for shortcuts\n")
		return b.String()
	}
	b.WriteString("────────────────────────────────────────\n")
	if s.lone() {
		b.WriteString(" " + hl + " ☐ " + s.qs[0].header + " " + unhl + "\n")
	} else {
		b.WriteString("←  ")
		for i, q := range s.qs {
			m := "☐"
			if s.answered(i) {
				m = "☒"
			}
			if i == s.active {
				b.WriteString(hl + " " + m + " " + q.header + " " + unhl + " ")
			} else {
				b.WriteString(" " + m + " " + q.header + "  ")
			}
		}
		if s.active == len(s.qs) {
			b.WriteString(hl + " ✔ Submit " + unhl + " →\n")
		} else {
			b.WriteString("✔ Submit  →\n")
		}
	}
	b.WriteString("\n")
	items := s.items()
	cur := func(i int) string {
		if i == s.cursor {
			return "❯ "
		}
		return "  "
	}
	if s.active == len(s.qs) {
		b.WriteString("\x1b[1mReview your answers\x1b[0m\n\n")
		all := true
		for i := range s.qs {
			all = all && s.answered(i)
		}
		if !all {
			b.WriteString("⚠ Some questions are unanswered\n\n")
		}
		for i, q := range s.qs {
			if s.answered(i) {
				b.WriteString(" ● " + q.question + "\n   → " + s.answer(i) + "\n")
			}
		}
		b.WriteString("\n" + cur(0) + "1. Submit answers\n" + cur(1) + "2. Cancel\n\n\n")
		b.WriteString("──────────────────────────────── demo ─\n")
		return b.String()
	}
	q := s.qs[s.active]
	b.WriteString("\x1b[1m" + q.question + "\x1b[0m\n\n")
	n := 0
	for i, it := range items {
		switch {
		case strings.HasPrefix(it, "opt:"):
			n++
			box := ""
			if q.multi {
				box = "[ ] "
				if q.checked[n-1] {
					box = "[✔] "
				}
			}
			fmt.Fprintf(&b, "%s\x1b[38;5;246m%d.\x1b[39m %s%s\n     \x1b[38;5;246m%s option\x1b[39m\n", cur(i), n, box, q.opts[n-1], q.opts[n-1])
		case it == "text":
			n++
			box := ""
			if q.multi {
				box = "[ ] "
				if q.textChecked {
					box = "[✔] "
				}
			}
			label := "Type something."
			if q.multi {
				label = "Type something"
			}
			if q.text != "" {
				label = q.text
			}
			fmt.Fprintf(&b, "%s%d. %s%s\n", cur(i), n, box, label)
		case it == "next":
			w := "Next"
			if s.active == len(s.qs)-1 {
				w = "Submit"
			}
			b.WriteString(cur(i) + "   " + w + "\n")
		case it == "chat":
			n++
			fmt.Fprintf(&b, "────────────────────────────────────────\n%s%d. Chat about this\n", cur(i), n)
		}
	}
	b.WriteString("\nEnter to select · Tab/Arrow keys to navigate · Esc to cancel\n")
	b.WriteString("──────────────────────────────── demo ─\n")
	return b.String()
}

func (s *simAsk) key(k string) error {
	n := len(s.keys)
	s.keys = append(s.keys, k)
	if s.onKey != nil {
		s.onKey(n, s)
	}
	if s.drop[n] || s.closed != "" {
		return nil
	}
	items := s.items()
	it := items[s.cursor]
	review := s.active == len(s.qs)
	var q *simQ
	if !review {
		q = &s.qs[s.active]
	}
	switch k {
	case "Down":
		s.cursor = min(s.cursor+1, len(items)-1)
	case "Up":
		s.cursor = max(s.cursor-1, 0)
	case "Right", "Left":
		if it == "text" || s.lone() {
			return nil // the caret moves
		}
		if k == "Right" {
			s.active = min(s.active+1, len(s.qs))
		} else {
			s.active = max(s.active-1, 0)
		}
		s.cursor = 0
	case "Space":
		switch {
		case it == "text":
			q.text += " "
		case strings.HasPrefix(it, "opt:") && q.multi:
			var o int
			fmt.Sscanf(it, "opt:%d", &o)
			q.checked[o-1] = !q.checked[o-1]
		}
	case "BSpace":
		if it == "text" && q.text != "" {
			r := []rune(q.text)
			q.text = string(r[:len(r)-1])
		}
	case "Enter":
		switch {
		case review:
			s.closed = map[bool]string{true: "submitted", false: "cancel"}[s.cursor == 0]
		case it == "chat":
			s.closed = "chat"
		case it == "next":
			s.active++
			s.cursor = 0
		case q.multi && it == "text":
			q.textChecked = !q.textChecked
		case q.multi:
			var o int
			fmt.Sscanf(it, "opt:%d", &o)
			q.checked[o-1] = !q.checked[o-1]
		default:
			if it == "text" {
				if q.text == "" {
					return nil
				}
				q.pick = -1
			} else {
				fmt.Sscanf(it, "opt:%d", &q.pick)
			}
			if s.lone() {
				s.closed = "submitted"
				return nil
			}
			s.active++
			s.cursor = 0
		}
	default:
		return errors.New("key the CLI has no use for: " + k)
	}
	return nil
}

func (s *simAsk) typ(text string) error {
	s.keys = append(s.keys, "text:"+text)
	if s.dropTyp || s.closed != "" || s.active == len(s.qs) {
		return nil
	}
	if s.items()[s.cursor] == "text" {
		q := &s.qs[s.active]
		q.text += text
		if q.multi {
			q.textChecked = true
		}
	}
	return nil
}

func (s *simAsk) driver(req AskRequest) *askDriver {
	return &askDriver{
		read:  func() (string, error) { return s.screen(), nil },
		key:   s.key,
		typ:   s.typ,
		sleep: func(time.Duration) {},
		req:   req,
	}
}

func colorFruit() *simAsk {
	return newSim(
		simQ{header: "Color", question: "Which color do you prefer?", opts: []string{"Red", "Blue"}, freeText: true},
		simQ{header: "Fruit", question: "Which fruits do you want? (select all that apply)", multi: true, opts: []string{"Apple", "Banana", "Cherry"}, freeText: true},
	)
}

func colorFruitReq(color []string, colorText string, fruit []string, fruitText string) AskRequest {
	return AskRequest{
		Tabs: []string{"Color", "Fruit"},
		Answers: []AskAnswer{
			{Tab: 1, Question: "Which fruits do you want? (select all that apply)", Options: fruit, Text: fruitText},
			{Tab: 0, Question: "Which color do you prefer?", Options: color, Text: colorText},
		},
		Submit: true,
	}
}

// The simulator itself must draw what the parser reads off the real CLI.
func TestSimDrawsWhatTheParserReads(t *testing.T) {
	s := colorFruit()
	a := parseAsk(s.screen())
	if a == nil || tabs(a) != "[Color] Fruit Submit" || a.TextOption != 3 || a.cursor != "opt:1" || !a.TextEmpty {
		t.Fatalf("%+v", a)
	}
	_ = s.key("Right")
	a = parseAsk(s.screen())
	if a == nil || tabs(a) != "Color [Fruit] Submit" || !a.MultiSelect || a.TextOption != 4 || len(a.items) != 6 {
		t.Fatalf("%+v", a)
	}
}

// Two questions, one of them multiSelect with typed text: every key, in
// order, and the answer the CLI ends up with.
func TestAskKeyPlan(t *testing.T) {
	s := colorFruit()
	d := s.driver(colorFruitReq([]string{"Blue"}, "", []string{"Apple", "Cherry"}, "kiwi"))
	if err := d.run(); err != nil {
		t.Fatalf("%v (keys %q)", err, s.keys)
	}
	want := []string{
		"Down", "Enter", // Color: Blue, on to Fruit
		"Space",                 // Apple
		"Down", "Down", "Space", // Cherry
		"Down", "text:kiwi", // the text, which ticks it
		"Up", "Up", "Up", "Right", // off the text option, then the Submit tab
		"Enter", // Submit answers
	}
	if !slices.Equal(s.keys, want) {
		t.Errorf("keys = %q\nwant   %q", s.keys, want)
	}
	if s.closed != "submitted" || s.answer(0) != "Blue" || s.answer(1) != "Apple, Cherry, kiwi" {
		t.Errorf("closed=%q answers %q %q", s.closed, s.answer(0), s.answer(1))
	}
	if !slices.Equal(d.pressed, want) {
		t.Errorf("pressed = %q", d.pressed)
	}
}

func TestAskKeyPlanOtherShapes(t *testing.T) {
	// free text for a single choice; boxes ticked before are unticked
	s := colorFruit()
	s.qs[1].checked[1] = true
	s.qs[1].text, s.qs[1].textChecked = "old", true
	d := s.driver(colorFruitReq(nil, "teal ok", []string{"Apple"}, ""))
	if err := d.run(); err != nil {
		t.Fatalf("%v (keys %q)", err, s.keys)
	}
	if s.closed != "submitted" || s.answer(0) != "teal ok" || s.answer(1) != "Apple" {
		t.Errorf("closed=%q answers %q %q keys %q", s.closed, s.answer(0), s.answer(1), s.keys)
	}
	if slices.Contains(s.keys, "Space") && s.keys[slices.Index(s.keys, "Space")-1] == "text:" {
		t.Error("Space pressed on the text option")
	}

	// a lone question: the pick sends it
	s = newSim(simQ{header: "Size", question: "Which size do you want?", opts: []string{"Small", "Large"}, freeText: true})
	d = s.driver(AskRequest{Tabs: []string{"Size"}, Answers: []AskAnswer{{Tab: 0, Question: "Which size do you want?", Options: []string{"Large"}}}, Submit: true})
	if err := d.run(); err != nil || !slices.Equal(s.keys, []string{"Down", "Enter"}) || s.closed != "submitted" || s.answer(0) != "Large" {
		t.Errorf("lone: %v keys %q closed %q", err, s.keys, s.closed)
	}

	// Chat about this, from whatever question is on screen
	s = colorFruit()
	d = s.driver(AskRequest{Tabs: []string{"Color", "Fruit"}, Chat: true})
	if err := d.run(); err != nil || s.closed != "chat" || !slices.Equal(s.keys, []string{"Down", "Down", "Down", "Enter"}) {
		t.Errorf("chat: %v keys %q closed %q", err, s.keys, s.closed)
	}

	// already on the Submit tab (answered in the terminal or by an earlier
	// pick): confirming is one Enter, after the screen was read
	s = colorFruit()
	s.qs[0].pick, s.qs[1].checked[0], s.active = 2, true, 2
	if err := s.driver(AskRequest{Tabs: []string{"Color", "Fruit"}, Submit: true}).run(); err != nil || s.closed != "submitted" || !slices.Equal(s.keys, []string{"Enter"}) {
		t.Errorf("confirm: %v keys %q closed %q", err, s.keys, s.closed)
	}
	// but not with a question left unanswered
	s = colorFruit()
	s.qs[1].checked[0], s.active = true, 2
	if err := s.driver(AskRequest{Tabs: []string{"Color", "Fruit"}, Submit: true}).run(); err == nil || s.closed != "" || len(s.keys) != 0 {
		t.Errorf("confirm unanswered: %v keys %q closed %q", err, s.keys, s.closed)
	}

	// not submitted unless asked: the answers stay on screen
	s = colorFruit()
	req := colorFruitReq([]string{"Red"}, "", []string{"Banana"}, "")
	req.Submit = false
	if err := s.driver(req).run(); err != nil || s.closed != "" || !s.answered(0) || !s.answered(1) {
		t.Errorf("no submit: %v closed %q", err, s.closed)
	}
}

// Every way the screen can differ from what the page showed stops the answer
// before the next key, and Submit is never pressed.
func TestAskStopsOnAMismatch(t *testing.T) {
	good := func() AskRequest { return colorFruitReq([]string{"Blue"}, "", []string{"Apple"}, "") }
	cases := []struct {
		name  string
		setup func(s *simAsk, r *AskRequest)
		max   int // keys that may have been pressed
	}{
		{"other tabs", func(s *simAsk, r *AskRequest) { r.Tabs = []string{"Color", "Nuts"} }, 0},
		{"one tab more", func(s *simAsk, r *AskRequest) { r.Tabs = []string{"Color", "Fruit", "Size"} }, 0},
		{"other question", func(s *simAsk, r *AskRequest) { r.Answers[1].Question = "Which color do you like?" }, 0},
		{"unknown option", func(s *simAsk, r *AskRequest) { r.Answers[1].Options = []string{"Purple"} }, 0},
		{"two picks for a single choice", func(s *simAsk, r *AskRequest) { r.Answers[1].Options = []string{"Red", "Blue"} }, 0},
		{"a key not taken", func(s *simAsk, r *AskRequest) { s.drop = map[int]bool{0: true} }, 1},
		{"the dialog changes", func(s *simAsk, r *AskRequest) {
			s.onKey = func(n int, s *simAsk) {
				if n == 1 {
					s.qs[1].question = "Something else entirely?"
				}
			}
		}, 2},
		{"the dialog closes", func(s *simAsk, r *AskRequest) {
			s.onKey = func(n int, s *simAsk) {
				if n == 2 {
					s.closed = "cancel"
				}
			}
		}, 3},
		{"the text does not appear", func(s *simAsk, r *AskRequest) {
			s.dropTyp = true
			r.Answers[1].Options, r.Answers[1].Text = nil, "teal"
		}, 3},
		{"the summary differs", func(s *simAsk, r *AskRequest) {
			s.onKey = func(n int, s *simAsk) {
				if s.keys[n] == "Right" {
					s.qs[0].pick = 1 // Red, though Blue was picked
				}
			}
		}, 4},
	}
	for _, c := range cases {
		s := colorFruit()
		r := good()
		c.setup(s, &r)
		err := r.check()
		if err == nil {
			err = s.driver(r).run()
		}
		var ae *APIError
		if !errors.As(err, &ae) {
			t.Errorf("%s: err = %v (keys %q)", c.name, err, s.keys)
			continue
		}
		if s.closed == "submitted" || len(s.keys) > c.max {
			t.Errorf("%s: went on after the mismatch: keys %q closed %q", c.name, s.keys, s.closed)
		}
		if !strings.ContainsFunc(ae.Msg, func(r rune) bool { return r >= 'ก' && r <= '๛' }) {
			t.Errorf("%s: error not in Thai: %q", c.name, ae.Msg)
		}
	}
}

func TestAskRequestCheck(t *testing.T) {
	bad := []AskRequest{
		{},
		{Tabs: []string{"A"}},
		{Tabs: []string{"A"}, Submit: false, Answers: nil},
		{Tabs: []string{"A"}, Answers: []AskAnswer{{Tab: 1, Question: "q", Options: []string{"x"}}}},
		{Tabs: []string{"A"}, Answers: []AskAnswer{{Tab: 0, Question: "q"}}},
		{Tabs: []string{"A"}, Answers: []AskAnswer{{Tab: 0, Question: "q", Text: "two\nlines"}}},
		{Tabs: []string{"A"}, Answers: []AskAnswer{{Tab: 0, Question: "q", Text: strings.Repeat("ก", 501)}}},
		{Tabs: []string{"A"}, Answers: []AskAnswer{{Tab: 0, Question: "q", Options: []string{"x"}}, {Tab: 0, Question: "q", Options: []string{"y"}}}},
		{Tabs: []string{"A"}, Chat: true, Submit: true},
	}
	for i, r := range bad {
		if err := r.check(); err == nil {
			t.Errorf("%d: %+v accepted", i, r)
		}
	}
	ok := AskRequest{Tabs: []string{"A", "B"}, Answers: []AskAnswer{{Tab: 1, Question: "b", Options: []string{"x"}}, {Tab: 0, Question: "a", Text: " hi\x07 "}}}
	if err := ok.check(); err != nil || ok.Answers[0].Tab != 0 || ok.Answers[0].Text != "hi" {
		t.Errorf("%v %+v", err, ok.Answers)
	}
}

// {option: n} is answered as before; {ask: ...} is checked before anything
// is looked up, and neither reaches the audit log in clear.
func TestSessionAnswerAPI(t *testing.T) {
	e := newEnv(t)
	tok, _ := e.store.Enable("t")
	path := apiPrefix + "/sessions/sess-0001/answer"
	for _, c := range []struct {
		body string
		code int
		msg  string
	}{
		{`{"option":2}`, 409, "หน้าต่าง"}, // reached AnswerPrompt: no pane for this session
		{`{"option":0}`, 400, "option"},
		{`{"option":2,"ask":{"tabs":["A"],"chat":true}}`, 400, "either"},
		{`{"ask":{"tabs":["A"],"answers":[{"tab":0,"question":"q"}]}}`, 400, "ยังไม่ได้เลือก"},
		{`{"ask":{"tabs":["A"],"answers":[{"tab":0,"question":"q","options":["Secret answer"]}]}}`, 409, "หน้าต่าง"},
	} {
		nextWrite(e)
		code, m, body := e.do(t, "POST", path, tok, c.body)
		if code != c.code || !strings.Contains(fmt.Sprint(m), c.msg) {
			t.Errorf("%s = %d %s", c.body, code, body)
		}
	}
	if a := auditText(t, e); !strings.Contains(a, "aiagent.sessionAsk") || strings.Contains(a, "Secret answer") {
		t.Errorf("audit:\n%s", a)
	}
}

// An admin answering a room's session: the whole answer reaches the room's
// own instance unchanged.
func TestAskAnswerReachesTheRoom(t *testing.T) {
	e, _, o := osUserEnv(t)
	admin, _ := e.store.Enable("t")
	_, _, _ = e.store.AddUser("anna", "anna")
	var got []string
	instance(t, o, "anna", func(w http.ResponseWriter, r *http.Request) {
		b := make([]byte, 4096)
		n, _ := r.Body.Read(b)
		got = append(got, r.Method+" "+strings.TrimPrefix(r.URL.Path, apiPrefix)+" "+string(b[:n]))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":"SUCCESS","details":{"sessions":[{"sessionId":"anna-sess","live":true,"messageVia":"agcmd"}]}}`))
	})
	body := `{"ask":{"tabs":["Color","Fruit"],"answers":[{"tab":0,"question":"Which color?","options":["Blue"]},{"tab":1,"question":"Which fruits?","options":["Apple"],"text":"kiwi"}],"submit":true},"sendId":"ask-0001-aaaa"}`
	code, _, out := e.do(t, "POST", apiPrefix+"/sessions/anna-sess/answer", admin, body)
	if code != 200 {
		t.Fatalf("= %d %s", code, out)
	}
	last := got[len(got)-1]
	if last != "POST /sessions/anna-sess/answer "+body {
		t.Errorf("the room got %q", last)
	}
}
