package world

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// Menus Claude Code 2.1.325 draws over its input box, captured from a real
// session in tmux (capture-pane -e -p, 220x50, 2026-11-28): /model, /theme,
// /permissions (search box, then the list), /config, /resume, /effort and the
// idle prompt. Before this only a numbered list was read, its question ran up
// into the conversation, and the plain /theme list was not read at all.

func menuFixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "menus", name+".ansi"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestReadPromptReadsClaudeCodeMenus(t *testing.T) {
	cases := []struct {
		file, question string
		opts           []string
		detail         string // of the current option
	}{
		{"model", "Select model Switch between Claude models. Your pick becomes the default for new sessions. For other/previous model names, specify with --model.",
			[]string{"1.Default (recommended)", "2.Opus", "3.Fable", "4.Sonnet", "❯5.Haiku ✔"}, "Haiku 5.5 · Fastest for quick answers"},
		{"theme", "Theme Choose the text style that looks best with your terminal",
			[]string{"1.Auto (match terminal)", "2.Dark mode", "3.Light mode", "4.Dark mode (colorblind-friendly)", "5.Light mode (colorblind-friendly)", "❯6.✔ Dark mode (ANSI colors only)", "7.Light mode (ANSI colors only)", "8.New custom theme…"}, ""},
	}
	for _, c := range cases {
		p := readPrompt(menuFixture(t, c.file))
		if p == nil {
			t.Errorf("%s: not read", c.file)
			continue
		}
		if p.Question != c.question {
			t.Errorf("%s: question = %q", c.file, p.Question)
		}
		if got := labels(p); !slices.Equal(got, c.opts) {
			t.Errorf("%s: options = %q", c.file, got)
		}
		if p.ReadOnly || p.Ask != nil {
			t.Errorf("%s: readOnly=%v ask=%v", c.file, p.ReadOnly, p.Ask)
		}
		for _, o := range p.Options {
			if o.Current && o.Detail != c.detail {
				t.Errorf("%s: detail = %q", c.file, o.Detail)
			}
		}
	}
}

// A menu with a search box, or one that is not a list (the /effort slider), is
// shown with a note and cannot be picked from the web: typed text would go to
// its filter, and the cursor may sit in the search box, not on a row.
func TestReadPromptShowsMenusThatNeedTypingAsReadOnly(t *testing.T) {
	cases := []struct{ file, question string }{
		{"permissions", "Permissions"},
		{"permissions_list", "Permissions"},
		{"config", ""},
		{"resume", "Resume session"},
		{"effort", "Effort"},
	}
	for _, c := range cases {
		p := readPrompt(menuFixture(t, c.file))
		if p == nil {
			t.Errorf("%s: not read", c.file)
			continue
		}
		if !p.ReadOnly || p.Note == "" {
			t.Errorf("%s: readOnly=%v note=%q", c.file, p.ReadOnly, p.Note)
		}
		if !strings.HasPrefix(p.Question, c.question) || strings.Contains(p.Question, "claude-mem") || strings.Contains(p.Question, "▔") {
			t.Errorf("%s: question = %q", c.file, p.Question)
		}
	}
	// a numbered list in the conversation above the box is not the menu
	listed := strings.Replace(sgrRE.ReplaceAllString(menuFixture(t, "resume"), ""), "❯ /model", "  1. add a button\n  2. write a test", 1)
	if !strings.Contains(listed, "2. write a test") {
		t.Fatal("anchor gone from the fixture")
	}
	if p := readPrompt(listed); p == nil || len(p.Options) != 0 || !p.ReadOnly {
		t.Errorf("list above /resume read as %+v", p)
	}
	if p := readPrompt(menuFixture(t, "idle")); p != nil {
		t.Errorf("idle prompt read as %+v", p)
	}
}

// /config prints "Type to filter … Esc to clear": text sent to that session
// would land in its filter, so it counts as a dialog.
func TestOnDialogSeesTheConfigFilter(t *testing.T) {
	if !onDialog(sgrRE.ReplaceAllString(menuFixture(t, "config"), "")) {
		t.Error("/config not seen as a dialog")
	}
	if onDialog(sgrRE.ReplaceAllString(menuFixture(t, "idle"), "")) {
		t.Error("idle prompt seen as a dialog")
	}
}

// simMenu moves the ❯ of a captured menu the way Claude Code does: one row per
// arrow, no wrap past the ends; Enter closes it.
type simMenu struct {
	lines   []string
	rows    []int // line index of each row
	cur     int
	stuck   bool // the arrows do nothing
	swap    string
	pressed []string
}

func newSimMenu(t *testing.T, name string) *simMenu {
	plain := sgrRE.ReplaceAllString(menuFixture(t, name), "")
	s := &simMenu{lines: strings.Split(plain, "\n")}
	p := readPrompt(plain)
	if p == nil {
		t.Fatalf("%s: not read", name)
	}
	// each row is the next line (from the bottom of the box) holding its label
	from := 0
	for i := len(s.lines) - 1; i >= 0; i-- {
		if strings.HasPrefix(strings.TrimSpace(s.lines[i]), "▔") {
			from = i
			break
		}
	}
	for _, o := range p.Options {
		for i := from; i < len(s.lines); i++ {
			if strings.Contains(s.lines[i], o.Label) {
				s.rows = append(s.rows, i)
				if o.Current {
					s.cur = len(s.rows) - 1
				}
				from = i + 1
				break
			}
		}
	}
	if len(s.rows) != len(p.Options) {
		t.Fatalf("%s: rows %v, prompt %+v", name, s.rows, p)
	}
	return s
}

func (s *simMenu) screen() (string, error) {
	if s.swap != "" {
		return s.swap, nil
	}
	return strings.Join(s.lines, "\n"), nil
}

func (s *simMenu) mark(i int, r rune) {
	l := []rune(s.lines[s.rows[i]])
	l[3] = r
	s.lines[s.rows[i]] = string(l)
}

func (s *simMenu) key(k string) error {
	s.pressed = append(s.pressed, k)
	to := s.cur
	switch k {
	case "Down":
		to++
	case "Up":
		to--
	case "Enter":
		s.swap = "❯ \n? for shortcuts"
		return nil
	}
	if s.stuck || to < 0 || to >= len(s.rows) {
		return nil
	}
	s.mark(s.cur, ' ')
	s.mark(to, '❯')
	s.cur = to
	return nil
}

func (s *simMenu) driver() *menuDriver {
	return &menuDriver{read: s.screen, key: s.key, sleep: func(time.Duration) {}}
}

func TestMenuDriverMovesOneVerifiedStepAtATime(t *testing.T) {
	cases := []struct {
		file string
		n    int
		keys []string
	}{
		{"model", 1, []string{"Up", "Up", "Up", "Up", "Enter"}},
		{"model", 5, []string{"Enter"}},
		{"theme", 2, []string{"Up", "Up", "Up", "Up", "Enter"}},
		{"theme", 8, []string{"Down", "Down", "Enter"}},
	}
	for _, c := range cases {
		s := newSimMenu(t, c.file)
		if err := s.driver().pick(c.n); err != nil {
			t.Errorf("%s %d: %v", c.file, c.n, err)
		}
		if !slices.Equal(s.pressed, c.keys) {
			t.Errorf("%s %d: keys %v, want %v", c.file, c.n, s.pressed, c.keys)
		}
	}
}

func TestMenuDriverStopsWithoutEnterWhenTheScreenDisagrees(t *testing.T) {
	var api *APIError
	// the cursor does not move: never press Enter on the wrong row
	s := newSimMenu(t, "theme")
	s.stuck = true
	if err := s.driver().pick(2); !errors.As(err, &api) || api.Status != 409 || slices.Contains(s.pressed, "Enter") {
		t.Errorf("stuck: %v %v", err, s.pressed)
	}
	// another menu came up in between
	s = newSimMenu(t, "theme")
	s.swap = sgrRE.ReplaceAllString(menuFixture(t, "model"), "")
	d := s.driver()
	d.want = readPrompt(sgrRE.ReplaceAllString(menuFixture(t, "theme"), ""))
	if err := d.pick(2); !errors.As(err, &api) || len(s.pressed) != 0 {
		t.Errorf("other menu: %v %v", err, s.pressed)
	}
	// a menu that needs typing is refused before any key
	for _, f := range []string{"permissions", "permissions_list", "effort"} {
		s := &simMenu{lines: strings.Split(sgrRE.ReplaceAllString(menuFixture(t, f), ""), "\n")}
		if err := s.driver().pick(1); !errors.As(err, &api) || api.Status != 409 || !strings.Contains(api.Msg, "เทอร์มินัล") || len(s.pressed) != 0 {
			t.Errorf("%s: %v %v", f, err, s.pressed)
		}
	}
	// no such row
	s = newSimMenu(t, "model")
	if err := s.driver().pick(7); !errors.As(err, &api) || api.Status != 400 || len(s.pressed) != 0 {
		t.Errorf("no row 7: %v %v", err, s.pressed)
	}
}

// A menu opened in a background job's relay pane (someone attached to
// aiagent-<id>) is read even when the agent view does not say "blocked".
func TestBackgroundSessionReadsAMenuOnItsRelayPane(t *testing.T) {
	e := bgEnv(t, "working")
	e.runner.relayUp = true
	e.runner.sessions = "aiagent-aaaa1111\t1764290000\n"
	e.runner.paneStart = "new-session -d -s aiagent-aaaa1111 claude attach aaaa1111"
	e.runner.dialogScreen = menuFixture(t, "theme")
	p := bgSession(t, e).Prompt
	if p == nil || len(p.Options) != 8 || p.ReadOnly {
		t.Fatalf("prompt = %+v", p)
	}
}

// A read-only menu is not a question to push to the phone.
func TestPushSkipsAReadOnlyMenu(t *testing.T) {
	snap := Snapshot{Sessions: []Session{{SessionID: bgID, Live: true, Prompt: &Prompt{Question: "Resume session", ReadOnly: true, Note: "x"}}}}
	for _, it := range pushItems(snap, nil) {
		if it.Kind == "answer" {
			t.Errorf("pushed %+v", it)
		}
	}
}
