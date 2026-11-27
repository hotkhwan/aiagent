package world

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// A background session (Claude Code's daemon job, kind=bg) draws its
// AskUserQuestion dialog in the daemon's own pty: there is no tmux pane to
// read until the relay attaches one. The question is in the transcript as an
// AskUserQuestion tool_use with no tool_result, and the agent view says
// "blocked". Before this the web showed only "รอคุณตอบ" and the phone only
// "Needs instructions", with nothing to pick (owner, 2026-11-28).

const bgID = "aaaa1111-0000-0000-0000-000000000000"

// askUse is an AskUserQuestion tool_use line in the shape Claude Code 2.1.325
// writes it (checked against a real transcript, 2026-11-28).
func askUse(id, input string) string {
	return `{"type":"assistant","timestamp":"2026-11-04T03:00:01Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"` + id + `","name":"AskUserQuestion","input":` + input + `}]}}`
}

const (
	askSizeInput = `{"questions":[{"question":"Which size do you want?","header":"Size","multiSelect":false,"options":[{"label":"Small","description":"Small size option"},{"label":"Large","description":"Large size option"}]}]}`
	askTwoInput  = `{"questions":[{"question":"For the new admin choice: what should picking it do?","header":"Admin role","multiSelect":false,"options":[{"label":"Admin = explicit list (Recommended)","description":"Only rooms you set."},{"label":"Admin adds a room","description":"Keep the current behaviour."},{"label":"Label only","description":"Just a label."}]},{"question":"Which pets do you have?","header":"Pets","multiSelect":true,"options":[{"label":"Cat"},{"label":"Dog"}]}]}`
)

func bgEnv(t *testing.T, state string, lines ...string) *env {
	t.Helper()
	e := newEnv(t)
	fakeProcPPID(t, e.proc, "5001", "51", "1")
	writeFile(t, filepath.Join(e.home, ".claude", "sessions", "5001.json"), `{"pid":5001,"sessionId":"`+bgID+`","cwd":"/root","procStart":"51","kind":"bg","status":"busy"}`)
	e.runner.agentsJSON = `[{"id":"aaaa1111","sessionId":"` + bgID + `","name":"bg one","kind":"background","state":"` + state + `"}]`
	all := append([]string{`{"type":"user","timestamp":"2026-11-04T03:00:00Z","message":{"role":"user","content":"ask me"}}`}, lines...)
	writeFile(t, filepath.Join(e.home, ".claude", "projects", "-root", bgID+".jsonl"), strings.Join(all, "\n")+"\n")
	return e
}

func bgSession(t *testing.T, e *env) Session {
	t.Helper()
	for _, s := range e.col.Snapshot(context.Background()).Sessions {
		if s.SessionID == bgID {
			return s
		}
	}
	t.Fatal("session not listed")
	return Session{}
}

func TestBackgroundSessionShowsItsPendingQuestion(t *testing.T) {
	cases := []struct {
		name      string
		state     string
		lines     []string
		question  string
		opts      []string
		tabs      string
		questions int
	}{
		{"one question", "blocked", []string{askUse("toolu_1", askSizeInput)},
			"Which size do you want?", []string{"1.Small", "2.Large", "3.Type something(text)"}, "[Size]", 1},
		{"two questions, the second multiSelect", "blocked", []string{askUse("toolu_2", askTwoInput)},
			"For the new admin choice: what should picking it do?",
			[]string{"1.Admin = explicit list (Recommended)", "2.Admin adds a room", "3.Label only", "4.Type something(text)"},
			"[Admin role] Pets Submit", 2},
		{"answered already", "blocked", []string{askUse("toolu_3", askSizeInput),
			`{"type":"user","timestamp":"2026-11-04T03:00:02Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_3","content":"Your questions have been answered"}]}}`},
			"", nil, "", 0},
		{"not blocked", "working", []string{askUse("toolu_4", askSizeInput)}, "", nil, "", 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := bgSession(t, bgEnv(t, c.state, c.lines...))
			if c.opts == nil {
				if s.Prompt != nil {
					t.Fatalf("prompt = %+v, want none", s.Prompt)
				}
				return
			}
			p := s.Prompt
			if p == nil || p.Ask == nil {
				t.Fatalf("no prompt for a blocked session with a pending AskUserQuestion (status %q)", s.Status)
			}
			if s.Status != StatusNeedsInput || s.MessageVia != "relay" {
				t.Errorf("status %q via %q", s.Status, s.MessageVia)
			}
			if p.Question != c.question || p.Ask.Question != c.question {
				t.Errorf("question = %q", p.Question)
			}
			if got := labels(p); !slices.Equal(got, c.opts) {
				t.Errorf("options = %q, want %q", got, c.opts)
			}
			if got := tabs(p.Ask); got != c.tabs {
				t.Errorf("tabs = %q, want %q", got, c.tabs)
			}
			if len(p.Ask.Questions) != c.questions || !p.Ask.Chat || p.Ask.Source != "transcript" {
				t.Errorf("questions=%d chat=%v source=%q", len(p.Ask.Questions), p.Ask.Chat, p.Ask.Source)
			}
			if p.Options[0].Detail == "" {
				t.Errorf("description lost: %+v", p.Options[0])
			}
		})
	}
}

// When the relay pane is already attached, its screen is read as for an agcmd
// pane: the cursor and ticks are the real ones.
func TestBackgroundSessionReadsAnAttachedRelayScreen(t *testing.T) {
	e := bgEnv(t, "blocked", askUse("toolu_1", askSizeInput))
	e.runner.relayUp = true
	e.runner.paneStart = "new-session -d -s aiagent-aaaa1111 claude attach aaaa1111"
	e.runner.dialogScreen = fixture(t, "single")
	p := bgSession(t, e).Prompt
	if p == nil || p.Ask == nil || p.Ask.Source != "" || p.Ask.cursor != "opt:1" || len(p.Ask.Questions) != 1 {
		t.Fatalf("prompt = %+v", p)
	}
}

// dialogRunner moves the cursor and closes the dialog the way the real CLI
// does, on the plain (escape-free) single-question screen.
type dialogRunner struct {
	*fakeRunner
}

func (d dialogRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	if name == "tmux" && len(args) > 0 && args[0] == "send-keys" && !slices.Contains(args, "-l") {
		d.mu.Lock()
		switch args[len(args)-1] {
		case "Down":
			d.dialogScreen = strings.Replace(strings.Replace(d.dialogScreen, "❯ 1. Small", "  1. Small", 1), "  2. Large", "❯ 2. Large", 1)
		case "Enter":
			d.dialogScreen = ""
		}
		// recorded here: the fake's own send-keys redraws an input box
		d.calls = append(d.calls, append([]string{name}, args...))
		d.mu.Unlock()
		return nil, nil
	}
	return d.fakeRunner.Run(ctx, name, args...)
}

func (d dialogRunner) RunIn(ctx context.Context, dir, in, name string, args ...string) ([]byte, error) {
	return d.Run(ctx, name, args...)
}

// {option: n} from the phone, and {ask: ...} from the web, reach a background
// session through the relay: `claude attach` in aiagent-<id>, the dialog read
// off that pane, then the keys.
func TestBackgroundSessionQuestionIsAnsweredThroughTheRelay(t *testing.T) {
	for _, body := range []string{
		`{"option":2}`,
		`{"ask":{"tabs":["Size"],"answers":[{"tab":0,"question":"Which size do you want?","options":["Large"]}],"submit":true}}`,
	} {
		t.Run(body, func(t *testing.T) {
			e := bgEnv(t, "blocked", askUse("toolu_1", askSizeInput))
			e.col.Runner = dialogRunner{e.runner}
			tok, _ := e.store.Enable("test")
			e.runner.dialogScreen = sgrRE.ReplaceAllString(fixture(t, "single"), "")
			nextWrite(e)
			code, m, _ := e.do(t, "POST", apiPrefix+"/sessions/"+bgID+"/answer", tok, body)
			if code != 200 {
				t.Fatalf("answer = %d %v", code, m)
			}
			var got []string
			for _, c := range e.runner.callsTo("tmux") {
				if c[0] == "new-session" || c[0] == "send-keys" {
					got = append(got, strings.Join(c, " "))
				}
			}
			want := []string{
				"new-session -d -s aiagent-aaaa1111 -x 220 -y 50 -c /root claude attach aaaa1111",
				"send-keys -t =aiagent-aaaa1111: Down",
				"send-keys -t =aiagent-aaaa1111: Enter",
			}
			if strings.Join(got, "|") != strings.Join(want, "|") {
				t.Fatalf("tmux =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
			}
		})
	}
}

// The phone answers one choice at a time: a choice that needs typing, or a
// dialog of several questions, is refused with a reason rather than guessed.
func TestRelayOptionRefusesWhatOneChoiceCannotAnswer(t *testing.T) {
	e := bgEnv(t, "blocked", askUse("toolu_1", askSizeInput))
	e.col.Runner = dialogRunner{e.runner}
	tok, _ := e.store.Enable("test")
	e.runner.dialogScreen = sgrRE.ReplaceAllString(fixture(t, "single"), "")
	nextWrite(e)
	if code, m, _ := e.do(t, "POST", apiPrefix+"/sessions/"+bgID+"/answer", tok, `{"option":3}`); code != 409 || !strings.Contains(fmt.Sprint(m), "ต้องพิมพ์") {
		t.Fatalf("free-text option = %d %v", code, m)
	}
	e.runner.dialogScreen = fixture(t, "tabs_first")
	nextWrite(e)
	if code, m, _ := e.do(t, "POST", apiPrefix+"/sessions/"+bgID+"/answer", tok, `{"option":1}`); code != 409 || !strings.Contains(fmt.Sprint(m), "หลายข้อ") {
		t.Fatalf("several questions = %d %v", code, m)
	}
	for _, c := range e.runner.callsTo("tmux") {
		if c[0] == "send-keys" {
			t.Fatalf("pressed %q", c)
		}
	}
}
