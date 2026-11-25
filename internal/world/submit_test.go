package world

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"
)

const boxRule = "\x1b[38;5;244m────────────────────────────────\x1b[39m"

// fakeBox draws a Claude Code prompt and reacts to the keys typed into it.
type fakeBox struct {
	now, showAt time.Duration // when the typed text appears
	slow        time.Duration
	drawnAt     time.Duration // the box is drawn only from here on
	box         string
	pending     string
	typed       []string
	enters      int
	dropEnters  int // Enters lost, as by a CLI that is still starting
	sent        []string
	suggestion  string
	dialogAfter int  // a dialog covers the box after this many Enters
	dialogAtTyp bool // a dialog opens as the text is typed
	vanish      bool
	clearAt     time.Duration // text somebody else typed leaves the box
}

func (f *fakeBox) draw() string {
	if f.pending != "" && f.now >= f.showAt {
		f.box, f.pending = f.box+f.pending, ""
	}
	if f.clearAt > 0 && f.now >= f.clearAt {
		f.box, f.clearAt = "", 0
	}
	if f.dialogAtTyp && len(f.typed) > 0 || f.dialogAfter > 0 && f.enters >= f.dialogAfter {
		return "Do you want to proceed?\n ❯ 1. Yes\n   2. No\n Esc to cancel"
	}
	if f.vanish && len(f.typed) > 0 {
		return boxRule + "\n! " + f.box + "\n" + boxRule
	}
	if f.now < f.drawnAt {
		return " ▐▛███▛█   Claude Code"
	}
	in := f.box
	if in == "" && f.suggestion != "" {
		in = "\x1b[7mT\x1b[0m\x1b[2m" + f.suggestion + "\x1b[0m"
	}
	return "❯ an earlier message\n● ok\n" + boxRule + "\n\x1b[39m❯\u00a0" + in + "\n" + boxRule + "\n  ⏵⏵ auto mode on"
}

func (f *fakeBox) typer() Typer {
	return Typer{Pane: "%1", Sleep: func(d time.Duration) { f.now += d },
		Tmux: func(args ...string) (string, error) {
			if args[0] == "capture-pane" {
				return f.draw(), nil
			}
			f.draw()
			f.enters++
			switch {
			case f.enters <= f.dropEnters:
			case strings.HasSuffix(f.box, `\`):
				f.box = strings.TrimSuffix(f.box, `\`) + "\n"
			case f.box != "":
				f.sent = append(f.sent, f.box)
				f.box = ""
			}
			return "", nil
		},
		Claude: true, Type: func(s string) error {
			if f.now < f.drawnAt {
				f.dropEnters++ // typed into a CLI not reading yet
			}
			f.typed = append(f.typed, s)
			f.pending, f.showAt = s, f.now+f.slow
			return nil
		}}
}

func TestSubmitSendsOnce(t *testing.T) {
	const msg = "แก้หน้าแรก\nบรรทัดสอง"
	for name, f := range map[string]*fakeBox{
		"prompt":                       {},
		"slow paste":                   {slow: 2 * time.Second},
		"still starting":               {drawnAt: 3 * time.Second},
		"one Enter lost":               {dropEnters: 1},
		"two Enters lost":              {dropEnters: 2},
		"a suggestion in the box":      {suggestion: "ry \"fix the tests\"", dropEnters: 1},
		"left there by an earlier try": {box: msg},
		"somebody else is typing":      {box: "/rename x", clearAt: time.Second},
	} {
		t.Run(name, func(t *testing.T) {
			held := f.box == msg
			if err := f.typer().Submit(msg); err != nil {
				t.Fatal(err)
			}
			if len(f.sent) != 1 || f.sent[0] != msg {
				t.Fatalf("the agent received %q, want the message once", f.sent)
			}
			if want := 1; (held && len(f.typed) != 0) || (!held && len(f.typed) != want) {
				t.Fatalf("typed %d times", len(f.typed))
			}
			if f.enters != f.dropEnters+1 {
				t.Fatalf("Enter pressed %d times, want %d", f.enters, f.dropEnters+1)
			}
		})
	}
}

func TestSubmitReportsTextThatStays(t *testing.T) {
	var ns *ErrNotSubmitted
	stuck := &fakeBox{dropEnters: 99}
	if err := stuck.typer().Submit("hello"); !errors.As(err, &ns) {
		t.Fatalf("err = %v, want ErrNotSubmitted", err)
	}
	if stuck.enters != 3 || len(stuck.typed) != 1 {
		t.Fatalf("%d Enters, typed %d times; want 3 and 1", stuck.enters, len(stuck.typed))
	}
	gone := &fakeBox{slow: time.Hour}
	if err := gone.typer().Submit("hello"); !errors.As(err, &ns) || gone.enters != 0 {
		t.Fatalf("err = %v, %d Enters; text that never showed must not be confirmed", err, gone.enters)
	}
}

func TestSubmitNeverAnswersADialog(t *testing.T) {
	// The message opened a dialog (as /model does): that Enter was enough.
	f := &fakeBox{dialogAfter: 1, dropEnters: 1}
	if err := f.typer().Submit("/model opus"); err != nil || f.enters != 1 {
		t.Fatalf("err = %v, %d Enters", err, f.enters)
	}
	open := &fakeBox{dialogAfter: 1, enters: 1}
	var od *ErrOnDialog
	if err := open.typer().Submit("hi"); !errors.As(err, &od) || len(open.typed) != 0 {
		t.Fatalf("err = %v, typed %v", err, open.typed)
	}
}

func TestSubmitDoesNotConfirmADialogThatOpensMeanwhile(t *testing.T) {
	f := &fakeBox{dialogAtTyp: true}
	var ns *ErrNotSubmitted
	if err := f.typer().Submit("hi"); !errors.As(err, &ns) || f.enters != 0 {
		t.Fatalf("err = %v, Enter pressed %d times into the dialog", err, f.enters)
	}
}

func TestSubmitTrailingBackslashIsNotANewline(t *testing.T) {
	f := &fakeBox{}
	if err := f.typer().Submit(`see C:\`); err != nil || len(f.sent) != 1 || f.enters != 1 {
		t.Fatalf("err = %v, sent %q in %d Enters", err, f.sent, f.enters)
	}
}

// Shell mode and CLIs drawn another way have no box to read: one Enter.
func TestSubmitWithoutABoxPressesEnterOnce(t *testing.T) {
	f := &fakeBox{vanish: true}
	if err := f.typer().Submit("!ls"); err != nil || f.enters != 1 {
		t.Fatalf("err = %v, %d Enters", err, f.enters)
	}
}

func TestInputBox(t *testing.T) {
	rule := "────────────────────"
	for name, c := range map[string]struct {
		screen, want string
		ok           bool
	}{
		"cursor on it":   {rule + "\n❯ \x1b[7mT\x1b[0m\x1b[2mry this\x1b[0m\n" + rule, "", true},
		"dim suggestion": {rule + "\n\x1b[38;5;246m❯\u00a0\x1b[2m\x1b[39mPress up to edit\x1b[0m\n" + rule, "", true},
		"paste block":    {rule + "\n❯\u00a0[Pasted text #1 +29 lines]\n" + rule + "\n paste again to expand", "[Pastedtext#1+29lines]", true},
		"two lines":      {rule + " name ─\n❯ ก ข\n  ค\n" + rule, "กขค", true},
		"grey 2 is text": {rule + "\n❯ \x1b[38;5;2mhello\x1b[0m\n" + rule, "hello", true},
		"no box":         {"› hello", "", false},
	} {
		if got, ok := inputBox(c.screen); got != c.want || ok != c.ok {
			t.Errorf("%s: inputBox = %q, %v; want %q, %v", name, got, ok, c.want, c.ok)
		}
	}
	if !holdsMessage("[Pastedtext#4+2lines]", "a\nb\nc") || holdsMessage("[Pastedtext#4+2lines]", "a\nb") {
		t.Error("a folded paste is recognised by its line count")
	}
}

type failRunner struct{ err error }

func (f failRunner) Run(context.Context, string, ...string) ([]byte, error) { return nil, f.err }

func TestAgcmdExitCodesReachTheUserAsMessages(t *testing.T) {
	for code, want := range map[string]string{"3": "AIAGENT_NOT_SUBMITTED", "4": "AIAGENT_DIALOG", "1": "AGCMD_BIN_FAILED"} {
		_, exit := exec.Command("sh", "-c", "exit "+code).Output()
		c := &Collector{AgcmdPath: "agcmd", Runner: failRunner{exit}}
		_, err := c.agcmd(t.Context(), "dispatch", "x", "y")
		var ae *APIError
		if !errors.As(err, &ae) || ae.Code != want {
			t.Errorf("exit %s: %v, want %s", code, err, want)
		}
	}
}
