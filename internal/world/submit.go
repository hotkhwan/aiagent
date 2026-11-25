// internal/world/submit.go
//
// Typing a message into an agent CLI and seeing that it left the input box.
//
// Enter after a paste is not always a submit: a CLI that is still starting
// keeps the text and drops the Enter, a trailing backslash turns Enter into a
// newline, and a busy one may take the paste later than any fixed pause. So the
// box is read before and after. The box is only understood for Claude Code;
// for a CLI drawn another way the keys are pressed as before and the
// transcript remains the only evidence.
package world

import (
	"context"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ErrNotSubmitted: the text was typed but the CLI did not take it.
type ErrNotSubmitted struct{ Msg string }

func (e *ErrNotSubmitted) Error() string { return e.Msg }

// ErrOnDialog: a question covers the input box, nothing was typed.
type ErrOnDialog struct{}

func (e *ErrOnDialog) Error() string {
	return "the agent is showing a question, and typing there would answer it; answer it first"
}

// Typer types one message into one pane.
type Typer struct {
	// Tmux runs one tmux command and returns its output.
	Tmux  func(args ...string) (string, error)
	Sleep func(time.Duration)
	Pane  string
	// Type puts the text in the pane, without Enter.
	Type func(text string) error
	// Claude marks a pane known to run Claude Code, whose box is waited for:
	// keys pressed before it is drawn are lost in part.
	Claude bool
}

const (
	submitPoll = 150 * time.Millisecond
	// submitStart: how long a CLI that is starting may take to draw its box.
	submitStart = 8 * time.Second
	// submitClear: how long text somebody else is typing may stay in the box.
	submitClear = 2 * time.Second
	// submitAppear: how long the CLI may take to show the pasted text.
	submitAppear = 5 * time.Second
	// submitLeave: how long after an Enter the text may stay before another.
	submitLeave   = 1500 * time.Millisecond
	submitRetries = 2
)

// Submit types text and presses Enter, and reports an error unless the input
// box let go of it. Text is typed at most once; only Enter is repeated.
func (t Typer) Submit(text string) error {
	// Enter after a backslash is a newline in Claude Code, not a submit.
	if strings.HasSuffix(text, `\`) {
		text += " "
	}
	before, known, dialog := t.look()
	for waited := time.Duration(0); t.Claude && !known && !dialog && waited < submitStart; waited += submitPoll {
		t.Sleep(submitPoll)
		before, known, dialog = t.look()
	}
	if dialog {
		return &ErrOnDialog{}
	}
	if !known {
		if err := t.Type(text); err != nil {
			return err
		}
		t.Sleep(400 * time.Millisecond)
		return t.enter()
	}
	// Text an earlier try left behind is submitted, never typed again.
	if !holdsMessage(before, text) {
		for waited := time.Duration(0); before != "" && waited < submitClear; waited += submitPoll {
			t.Sleep(submitPoll)
			if before, _, dialog = t.look(); dialog {
				return &ErrOnDialog{}
			}
		}
		if err := t.Type(text); err != nil {
			return err
		}
	}
	held, boxed := t.settled(before, text)
	if !boxed {
		// The text changed the box into something else (shell mode).
		return t.enter()
	}
	if held == "" {
		return &ErrNotSubmitted{"the agent CLI did not take the text (it may be starting or busy); it was not submitted"}
	}
	for try := 0; ; try++ {
		// A dialog that opened meanwhile would take the Enter as its answer.
		if _, _, dialog := t.look(); dialog {
			if try > 0 {
				return nil // the text left the box and opened it
			}
			return &ErrNotSubmitted{"a question opened in the agent CLI before the text was submitted"}
		}
		if err := t.enter(); err != nil {
			return err
		}
		for waited := time.Duration(0); waited < submitLeave; waited += submitPoll {
			t.Sleep(submitPoll)
			now, known, dialog := t.look()
			if dialog || known && !strings.HasPrefix(now, strings.TrimRight(held, `\`)) {
				return nil
			}
			if !known {
				return nil // another screen: the CLI acted on the text
			}
		}
		if try == submitRetries {
			return &ErrNotSubmitted{"the text is still in the agent's input box and was not submitted"}
		}
	}
}

// settled waits for the typed text to show up in the box and stop changing. The
// text is empty when it never did; boxed is false when the box itself is gone.
func (t Typer) settled(before, text string) (held string, boxed bool) {
	last, gone := "", 0
	for waited := time.Duration(0); waited < submitAppear; waited += submitPoll {
		t.Sleep(submitPoll)
		now, known, dialog := t.look()
		if dialog {
			return "", true
		}
		if !known {
			if gone++; gone == 4 {
				return "", false
			}
			continue
		}
		gone = 0
		if now != "" && now == last && (now != before || holdsMessage(now, text)) {
			return now, true
		}
		last = now
	}
	return "", true
}

func (t Typer) enter() error {
	_, err := t.Tmux("send-keys", "-t", t.Pane, "Enter")
	return err
}

// look reads the input box and whether a dialog is open.
func (t Typer) look() (box string, known, dialog bool) {
	screen, err := t.Tmux("capture-pane", "-e", "-p", "-t", t.Pane)
	if err != nil {
		return "", false, false
	}
	box, known = inputBox(screen)
	return box, known, onDialog(sgrRE.ReplaceAllString(screen, ""))
}

var (
	sgrRE     = regexp.MustCompile("\x1b\\[([0-9;:]*)m")
	pastedRE  = regexp.MustCompile(`^\[Pastedtext#\d+(?:\+(\d+)lines)?\]$`)
	boxRuleRE = regexp.MustCompile(`^─{8,}`)
)

// inputBox is the text a Claude Code input box holds, without white space. The
// suggestion drawn dim in an empty box is not text. ok is false when the
// screen shows no such box.
func inputBox(screen string) (text string, ok bool) {
	var all, bright []string
	dim := false
	for _, l := range strings.Split(screen, "\n") {
		var a, b strings.Builder
		pos := 0
		add := func(s string) {
			a.WriteString(s)
			if !dim {
				b.WriteString(s)
			}
		}
		for _, m := range sgrRE.FindAllStringSubmatchIndex(l, -1) {
			add(l[pos:m[0]])
			dim = sgrDim(l[m[2]:m[3]], dim)
			pos = m[1]
		}
		add(l[pos:])
		all, bright = append(all, a.String()), append(bright, b.String())
	}
	bottom := -1
	for i := len(all) - 1; i >= 0; i-- {
		if !boxRuleRE.MatchString(strings.TrimSpace(all[i])) {
			continue
		}
		if bottom < 0 {
			bottom = i
			continue
		}
		if i+1 >= bottom || !strings.HasPrefix(strings.TrimSpace(all[i+1]), "❯") {
			return "", false
		}
		squash := func(ls []string) string {
			s := strings.Join(strings.Fields(strings.Join(ls, " ")), "")
			return strings.TrimPrefix(s, "❯")
		}
		box, full := squash(bright[i+1:bottom]), squash(all[i+1:bottom])
		// a drawn cursor takes the dim off the suggestion's first letter
		if len([]rune(box)) == 1 && len([]rune(full)) > 1 {
			box = ""
		}
		return box, true
	}
	return "", false
}

// sgrDim follows the dim attribute through one SGR sequence.
func sgrDim(params string, dim bool) bool {
	f := strings.Split(params, ";")
	for i := 0; i < len(f); i++ {
		switch f[i] {
		case "", "0", "22":
			dim = false
		case "2":
			dim = true
		case "38", "48", "58": // a colour: 5;n or 2;r;g;b
			if i+1 < len(f) && f[i+1] == "5" {
				i += 2
			} else if i+1 < len(f) && f[i+1] == "2" {
				i += 4
			}
		}
	}
	return dim
}

// holdsMessage reports that the box already holds exactly this message, in
// full or as the block a long paste is folded into.
func holdsMessage(box, text string) bool {
	if box == "" {
		return false
	}
	if box == strings.Join(strings.Fields(text), "") {
		return true
	}
	m := pastedRE.FindStringSubmatch(box)
	if m == nil {
		return false
	}
	n, _ := strconv.Atoi(m[1])
	return n == strings.Count(text, "\n")
}

// submit is Submit for the service's own tmux.
func (c *Collector) submit(ctx context.Context, pane, text string) error {
	tmux := func(args ...string) (string, error) { return c.tmux(ctx, args...) }
	return Typer{Tmux: tmux, Sleep: c.sleep, Pane: pane, Type: func(s string) error {
		_, err := tmux("send-keys", "-t", pane, "-l", "--", s)
		return err
	}}.Submit(text)
}
