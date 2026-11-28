package world

import (
	"context"
	"errors"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// 2026-11-28, room gon: an admin message waited in the outbox while the agent
// was busy and was typed in while an AskUserQuestion dialog was open, so it
// landed in the dialog's "Type something" row. A queued message is never
// typed while a dialog is on screen, and it is not given up on either: it
// waits for the dialog to close.
func TestOutboxHoldsAMessageWhileADialogIsOpen(t *testing.T) {
	const msg = "จาก admin: merge แล้ว ทำงานต่อได้เลย"
	e := outboxEnv(t)
	if _, err := e.svc.Enqueue(outboxSession, msg, e.now); err != nil {
		t.Fatal(err)
	}
	e.runner.dialogScreen = fixture(t, "tabs_first")

	for i := 0; i < 2; i++ {
		if sent := e.svc.FlushOutbox(context.Background(), e.now); sent != 0 {
			t.Fatalf("sent %d while the dialog was open", sent)
		}
	}
	for _, c := range e.runner.callsTo("tmux") {
		if c[0] == "send-keys" || c[0] == "paste-buffer" {
			t.Fatalf("a key reached the pane while a dialog was open: %q", c)
		}
	}
	got := e.svc.PendingFor(outboxSession)
	if len(got) != 1 || got[0].State != PendingWaiting {
		t.Fatalf("the message must stay queued while the dialog is open; got %+v", got)
	}

	e.runner.dialogScreen = ""
	if sent := e.svc.FlushOutbox(context.Background(), e.now); sent != 1 {
		t.Fatalf("sent %d after the dialog closed, want 1", sent)
	}
	if n := timesTyped(e, msg); n != 1 {
		t.Fatalf("typed %d times, want once", n)
	}
	if got := e.svc.PendingFor(outboxSession); len(got) != 0 {
		t.Fatalf("delivered, yet still queued: %+v", got)
	}
}

// strayBox is an idle Claude Code prompt that, as the text is pasted, turns
// into an AskUserQuestion dialog that takes the paste into its free-text row:
// the race the room hit, a busy CLI drawing the tool's dialog while the paste
// was on its way.
type strayBox struct {
	text    string // what the free-text row holds
	caret   bool   // the caret is at the end of the row
	open    bool   // the dialog is on screen
	keys    []string
	noClear bool // ctrl+u is not taken
}

func (f *strayBox) screen() string {
	if !f.open {
		return "❯ earlier\n" + boxRule + "\n❯ \n" + boxRule + "\n  ⏵⏵ auto mode on"
	}
	row := "Type something."
	cur := "❯"
	if f.text != "" {
		row = f.text
	}
	return "● earlier work\n" + boxRule + "\n←  " + hl + " ☐ Color " + unhl + "  ☐ Pet  ✔ Submit  →\n\nPick a color?\n\n" +
		"  1. Red\n     Choose Red\n  2. Blue\n     Choose Blue\n" + cur + " 3. " + row + "\n" + boxRule +
		"\n  4. Chat about this\n\nEnter to select · Tab/Arrow keys to navigate · ctrl+g to edit in Vim · Esc to cancel\n" + boxRule
}

func (f *strayBox) typer() Typer {
	return Typer{Pane: "%1", Claude: true, Sleep: func(time.Duration) {},
		Tmux: func(args ...string) (string, error) {
			if args[0] == "capture-pane" {
				return f.screen(), nil
			}
			k := args[len(args)-1]
			f.keys = append(f.keys, k)
			switch {
			case !f.open:
			case k == "C-e":
				f.caret = true
			case k == "C-u" && f.caret && !f.noClear:
				f.text = ""
			}
			return "", nil
		},
		Type: func(s string) error {
			f.keys = append(f.keys, "text:"+s)
			f.open, f.text, f.caret = true, f.text+s, true
			return nil
		}}
}

// The text that landed in the dialog is taken out again, and the send is
// reported as held by a dialog, so the queue keeps it for later. Enter is
// never pressed: on the dialog it would answer the question with the message.
func TestSubmitTakesBackTextThatLandedInADialog(t *testing.T) {
	const msg = "from admin: merged, continue"
	f := &strayBox{}
	err := f.typer().Submit(msg)
	var od *ErrOnDialog
	if !errors.As(err, &od) {
		t.Fatalf("err = %v, want ErrOnDialog (held)", err)
	}
	if f.text != "" {
		t.Fatalf("the dialog's free-text row still holds %q", f.text)
	}
	if slices.Contains(f.keys, "Enter") {
		t.Fatalf("Enter pressed on the dialog: %q", f.keys)
	}
	if want := []string{"text:" + msg, "C-e", "C-u"}; !slices.Equal(f.keys, want) {
		t.Errorf("keys = %q, want %q", f.keys, want)
	}
}

// Text that cannot be taken out is reported as such, never retried: typing
// it again would put it there twice.
func TestSubmitReportsTextLeftInADialog(t *testing.T) {
	f := &strayBox{noClear: true}
	err := f.typer().Submit("from admin: merged")
	var td *ErrTypedIntoDialog
	if !errors.As(err, &td) {
		t.Fatalf("err = %v, want ErrTypedIntoDialog", err)
	}
	if slices.Contains(f.keys, "Enter") {
		t.Fatalf("Enter pressed on the dialog: %q", f.keys)
	}
	ae := typedIntoDialogError()
	if ae.Status != 409 || !strings.Contains(ae.Msg, "Type something") {
		t.Errorf("the owner must be told to clear the row in the terminal: %+v", ae)
	}
}

// Text somebody else typed into the row is not ours to delete.
func TestSubmitLeavesOtherTextInADialogAlone(t *testing.T) {
	f := &strayBox{text: "owner's note "}
	f.open = false
	tp := f.typer()
	tp.Type = func(s string) error {
		f.keys = append(f.keys, "text:"+s)
		f.open, f.text, f.caret = true, f.text+s, true
		return nil
	}
	err := tp.Submit("hello")
	var ns *ErrNotSubmitted
	if !errors.As(err, &ns) || slices.Contains(f.keys, "C-u") {
		t.Fatalf("err = %v keys %q: only the text this send typed may be deleted", err, f.keys)
	}
}

func TestAgcmdTypedIntoDialogExitCode(t *testing.T) {
	c := &Collector{AgcmdPath: "agcmd", Runner: failRunner{exitErr(t, ExitTypedIntoDialog)}}
	_, err := c.agcmd(t.Context(), "dispatch", "x", "y")
	var ae *APIError
	if !errors.As(err, &ae) || ae.Code != "AIAGENT_TYPED_INTO_DIALOG" {
		t.Errorf("err = %v", err)
	}
}

func exitErr(t *testing.T, code int) error {
	t.Helper()
	_, err := exec.Command("sh", "-c", "exit "+strconv.Itoa(code)).Output()
	return err
}

// Screens captured from Claude Code 2.1.325 with a message pasted into the
// free-text row of an AskUserQuestion dialog, and after ctrl+u.
func TestParseAskStrayText(t *testing.T) {
	for _, c := range []struct {
		file, cursor, row string
		empty             bool
	}{
		{"tabs_stray_pasted", "opt:3", "from admin: merged, continue", false},
		{"tabs_stray_checked", "opt:3", "from admin: merged, continue X", false},
		{"tabs_stray_cursor_off", "opt:1", "from admin: merged, continue", false},
		{"tabs_stray_cleared", "opt:3", "Type something.", true},
		{"tabs_stray_multiline", "opt:3", "", false},
		{"tabs_stray_multiline_cut", "opt:3", "", false},
	} {
		a := parseAsk(fixture(t, c.file))
		if a == nil || a.TextOption != 3 || a.cursor != c.cursor || a.TextEmpty != c.empty {
			t.Errorf("%s: %+v", c.file, a)
			continue
		}
		if c.row != "" && strings.TrimSpace(a.labels[3]) != c.row {
			t.Errorf("%s: row = %q, want %q", c.file, a.labels[3], c.row)
		}
	}
	// the rows ctrl+u took away are seen to go
	full, cut := parseAsk(fixture(t, "tabs_stray_multiline")), parseAsk(fixture(t, "tabs_stray_multiline_cut"))
	if askRow(full) == askRow(cut) || !strings.Contains(askRow(full), "second line here") {
		t.Errorf("rows: %q / %q", askRow(full), askRow(cut))
	}
	// a ticked option of a single choice is read, though the row holds text
	if a := parseAsk(fixture(t, "tabs_stray_checked")); a.opt(1).Label != "Red" {
		t.Errorf("option 1 = %+v", a.opt(1))
	}
}

// The room's dialog had a message in its free-text row that the owner never
// typed, and the owner picked an option from the app. The stray text is
// deleted first, then the option is picked, and the summary says the option.
func TestAskClearsStrayTextBeforePicking(t *testing.T) {
	s := colorFruit()
	s.textWins = true
	s.qs[0].text = "from admin: merged, continue"
	d := s.driver(colorFruitReq([]string{"Blue"}, "", []string{"Apple"}, ""))
	if err := d.run(); err != nil {
		t.Fatalf("%v (keys %q)", err, s.keys)
	}
	if s.closed != "submitted" || s.answer(0) != "Blue" || s.qs[0].text != "" {
		t.Fatalf("closed=%q answer %q row %q keys %q", s.closed, s.answer(0), s.qs[0].text, s.keys)
	}
	i := slices.Index(s.keys, "C-u")
	if i < 0 || slices.Index(s.keys, "Enter") < i {
		t.Errorf("the text must go before anything is picked: %q", s.keys)
	}
}

// Text that will not go stops the answer before anything is picked, and the
// owner is told what the text is and to clear it in the terminal.
func TestAskStopsOnStrayTextItCannotClear(t *testing.T) {
	s := colorFruit()
	s.noClear = true
	s.qs[0].text = "from admin: merged"
	err := s.driver(colorFruitReq([]string{"Blue"}, "", []string{"Apple"}, "")).run()
	var ae *APIError
	if !errors.As(err, &ae) || !strings.Contains(ae.Msg, "from admin: merged") || !strings.Contains(ae.Msg, "เทอร์มินัล") {
		t.Fatalf("err = %v", err)
	}
	if slices.Contains(s.keys, "Enter") || slices.Contains(s.keys, "Space") {
		t.Fatalf("picked something with stray text in the row: %q", s.keys)
	}
}

// A single choice answered already draws "2. Blue ✔". Picking Blue again from
// the app finds it, and the summary that lists "Blue" matches the pick.
func TestAskRepicksATickedOption(t *testing.T) {
	s := colorFruit()
	s.qs[0].pick = 2
	if err := s.driver(colorFruitReq([]string{"Blue"}, "", []string{"Apple"}, "")).run(); err != nil {
		t.Fatalf("%v (keys %q)", err, s.keys)
	}
	if s.closed != "submitted" || s.answer(0) != "Blue" {
		t.Fatalf("closed %q answer %q", s.closed, s.answer(0))
	}
	if p := readPrompt(s.screen()); p != nil {
		t.Fatalf("still a dialog: %+v", p)
	}
}

// Replacing text in the row, as when the owner answers with text of their
// own, uses the same keys, so a long or wrapped text is no longer refused.
func TestAskReplacesLongTextInTheRow(t *testing.T) {
	s := colorFruit()
	s.qs[0].text = strings.Repeat("ยาวมาก ", 120)
	if err := s.driver(colorFruitReq(nil, "teal", []string{"Apple"}, "")).run(); err != nil {
		t.Fatalf("%v (keys %q)", err, s.keys)
	}
	if s.answer(0) != "teal" || slices.Contains(s.keys, "BSpace") {
		t.Fatalf("answer %q keys %q", s.answer(0), s.keys)
	}
}
