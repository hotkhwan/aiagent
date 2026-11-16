package planner

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ErrBusy means the PM is already working on the same thing.
var ErrBusy = errors.New("the PM is still working on the last request")

const (
	evUserMessage = "userMessage"
	evReply       = "pmReply"
	evFailed      = "pmFailed"
	evConfirmed   = "proposalConfirmed"
	evDismissed   = "proposalDismissed"
	evReset       = "threadReset"
	evAdviceStart = "advisoryStarted"
	evAdviceDone  = "advisoryDone"
	evAdviceFail  = "advisoryFailed"

	interrupted = "the service restarted before the PM answered"
	// maxMessages bounds what one view carries; the log keeps everything.
	maxMessages = 200
)

type threadEvent struct {
	Seq        int64     `json:"seq"`
	At         time.Time `json:"at"`
	Type       string    `json:"type"`
	Text       string    `json:"text,omitempty"`
	Plan       *Plan     `json:"plan,omitempty"`
	Call       *Call     `json:"call,omitempty"`
	ProposalID string    `json:"proposalId,omitempty"`
	TaskID     string    `json:"taskId,omitempty"`
	Key        string    `json:"key,omitempty"`
	Advice     *Advice   `json:"advice,omitempty"`
}

// ProposalView is a proposal and what the user did with it.
type ProposalView struct {
	ID string `json:"id"`
	Proposal
	State  string `json:"state"` // open | confirmed | dismissed
	TaskID string `json:"taskId,omitempty"`
}

// Message is one turn of the conversation as the UI shows it.
type Message struct {
	ID        string         `json:"id"`
	Role      string         `json:"role"` // user | pm
	At        time.Time      `json:"at"`
	Text      string         `json:"text"`
	Questions []string       `json:"questions,omitempty"`
	Proposals []ProposalView `json:"proposals,omitempty"`
	Failed    bool           `json:"failed,omitempty"`
	Call      *Call          `json:"call,omitempty"`
}

// View is the conversation since the last reset.
type View struct {
	Thinking      bool       `json:"thinking"`
	ThinkingSince *time.Time `json:"thinkingSince,omitempty"`
	Messages      []Message  `json:"messages"`
}

// AdvisoryView is the reviewer's note for one attempt, or where it stands.
type AdvisoryView struct {
	State  string    `json:"state"` // running | done | failed
	At     time.Time `json:"at"`
	Advice *Advice   `json:"advice,omitempty"`
	Error  string    `json:"error,omitempty"`
	Call   *Call     `json:"call,omitempty"`
}

// Thread is the PM's conversation and its advisory notes, kept as an
// append-only log: one JSON object per line, fsynced before a write returns.
// Like the task log, the file is the source of truth and the view is folded
// from it. Only the service writes it.
type Thread struct {
	path string
	Now  func() time.Time

	mu     sync.Mutex
	events []threadEvent
}

// OpenThread loads the log at path, creating it if it does not exist. Work
// that was in flight when the service last stopped is recorded as failed.
func OpenThread(path string) (*Thread, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	t := &Thread{path: path, Now: time.Now}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	// Half a line left by a crash mid-append is cut off, so the next write
	// starts on a clean line.
	if i := bytes.LastIndexByte(data, '\n'); i < len(data)-1 {
		data = data[:i+1]
		if err := f.Truncate(int64(len(data))); err != nil {
			return nil, err
		}
	}
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 64<<10), 16<<20)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var e threadEvent
		if err := json.Unmarshal(line, &e); err != nil {
			return nil, fmt.Errorf("pm log is corrupt: %s", path)
		}
		t.events = append(t.events, e)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	if t.view().Thinking {
		if err := t.append(threadEvent{Type: evFailed, Text: interrupted}); err != nil {
			return nil, err
		}
	}
	for key, a := range t.advisories() {
		if a.State == "running" {
			if err := t.append(threadEvent{Type: evAdviceFail, Key: key, Text: interrupted}); err != nil {
				return nil, err
			}
		}
	}
	return t, nil
}

// append writes one event as a whole line. The caller holds t.mu.
func (t *Thread) append(e threadEvent) error {
	e.At = t.Now().UTC()
	e.Seq = 1
	if n := len(t.events); n > 0 {
		e.Seq = t.events[n-1].Seq + 1
	}
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(t.path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(append(b, '\n')); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	// Keep what the log holds rather than what the caller passed: a caller's
	// string may sit in a buffer that is reused after the call.
	var own threadEvent
	if err := json.Unmarshal(b, &own); err != nil {
		return err
	}
	t.events = append(t.events, own)
	return nil
}

func proposalID(seq int64, n int) string { return fmt.Sprintf("P%d-%d", seq, n+1) }

// view folds the events since the last reset. The caller holds t.mu.
func (t *Thread) view() View {
	start := 0
	for i, e := range t.events {
		if e.Type == evReset {
			start = i + 1
		}
	}
	v := View{Messages: []Message{}}
	where := map[string][2]int{} // proposal id → message, proposal index
	for _, e := range t.events[start:] {
		switch e.Type {
		case evUserMessage:
			at := e.At
			v.Thinking, v.ThinkingSince = true, &at
			v.Messages = append(v.Messages, Message{ID: fmt.Sprintf("M%d", e.Seq), Role: "user", At: e.At, Text: e.Text})
		case evReply:
			v.Thinking, v.ThinkingSince = false, nil
			m := Message{ID: fmt.Sprintf("M%d", e.Seq), Role: "pm", At: e.At, Call: e.Call}
			if e.Plan != nil {
				m.Text, m.Questions = e.Plan.Reply, e.Plan.Questions
				for i, p := range e.Plan.Proposals {
					id := proposalID(e.Seq, i)
					where[id] = [2]int{len(v.Messages), i}
					m.Proposals = append(m.Proposals, ProposalView{ID: id, Proposal: p, State: "open"})
				}
			}
			v.Messages = append(v.Messages, m)
		case evFailed:
			v.Thinking, v.ThinkingSince = false, nil
			v.Messages = append(v.Messages, Message{ID: fmt.Sprintf("M%d", e.Seq), Role: "pm", At: e.At, Text: e.Text, Failed: true, Call: e.Call})
		case evConfirmed, evDismissed:
			at, ok := where[e.ProposalID]
			if !ok {
				continue
			}
			p := &v.Messages[at[0]].Proposals[at[1]]
			if e.Type == evConfirmed {
				p.State, p.TaskID = "confirmed", e.TaskID
			} else {
				p.State = "dismissed"
			}
		}
	}
	if n := len(v.Messages); n > maxMessages {
		v.Messages = v.Messages[n-maxMessages:]
	}
	return v
}

// advisories folds the reviewer notes; a reset does not clear them. The
// caller holds t.mu.
func (t *Thread) advisories() map[string]AdvisoryView {
	out := map[string]AdvisoryView{}
	for _, e := range t.events {
		switch e.Type {
		case evAdviceStart:
			out[e.Key] = AdvisoryView{State: "running", At: e.At}
		case evAdviceDone:
			out[e.Key] = AdvisoryView{State: "done", At: e.At, Advice: e.Advice, Call: e.Call}
		case evAdviceFail:
			out[e.Key] = AdvisoryView{State: "failed", At: e.At, Error: e.Text, Call: e.Call}
		}
	}
	return out
}

// View is the conversation as it stands.
func (t *Thread) View() View {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.view()
}

// Ask records the user's message and marks the PM as thinking. It returns
// the message's id.
func (t *Thread) Ask(text string) (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.view().Thinking {
		return "", ErrBusy
	}
	if err := t.append(threadEvent{Type: evUserMessage, Text: text}); err != nil {
		return "", err
	}
	return fmt.Sprintf("M%d", t.events[len(t.events)-1].Seq), nil
}

// Reply records the PM's plan for the message it was thinking about.
func (t *Thread) Reply(plan Plan, call Call) error {
	return t.answer(threadEvent{Type: evReply, Plan: &plan, Call: &call})
}

// Fail records that the PM could not answer, and why.
func (t *Thread) Fail(reason string, call Call) error {
	return t.answer(threadEvent{Type: evFailed, Text: reason, Call: &call})
}

func (t *Thread) answer(e threadEvent) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.view().Thinking {
		return errors.New("no question is waiting for an answer")
	}
	return t.append(e)
}

func (t *Thread) find(id string) (ProposalView, []ProposalView, bool) {
	p, m, ok := t.findIn(id)
	return p, m.Proposals, ok
}

// findIn returns a proposal and the message that carried it.
func (t *Thread) findIn(id string) (ProposalView, Message, bool) {
	for _, m := range t.view().Messages {
		for _, p := range m.Proposals {
			if p.ID == id {
				return p, m, true
			}
		}
	}
	return ProposalView{}, Message{}, false
}

// ConfirmKey is the idempotency key for turning a proposal into a task: the
// same on every retry, and — because it carries the moment the plan was
// written — not shared with a proposal of another log that has the same id.
func (t *Thread) ConfirmKey(id string) (string, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	_, m, ok := t.findIn(id)
	if !ok {
		return "", false
	}
	return fmt.Sprintf("pm:%s@%d", id, m.At.UnixNano()), true
}

// Proposal returns one proposal and the others of the same plan, which its
// dependsOn positions refer to.
func (t *Thread) Proposal(id string) (ProposalView, []ProposalView, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.find(id)
}

// Confirm records that the user turned a proposal into taskID. Confirming
// again with the same task changes nothing.
func (t *Thread) Confirm(id, taskID string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	p, _, ok := t.find(id)
	switch {
	case !ok:
		return fmt.Errorf("no proposal %s", id)
	case p.State == "confirmed" && p.TaskID == taskID:
		return nil
	case p.State != "open":
		return fmt.Errorf("proposal %s is already %s", id, p.State)
	}
	return t.append(threadEvent{Type: evConfirmed, ProposalID: id, TaskID: taskID})
}

// Dismiss records that the user does not want a proposal.
func (t *Thread) Dismiss(id string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	p, _, ok := t.find(id)
	switch {
	case !ok:
		return fmt.Errorf("no proposal %s", id)
	case p.State == "dismissed":
		return nil
	case p.State != "open":
		return fmt.Errorf("proposal %s is already %s", id, p.State)
	}
	return t.append(threadEvent{Type: evDismissed, ProposalID: id})
}

// Reset starts a new conversation. The log keeps the old one.
func (t *Thread) Reset() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.view().Thinking {
		return ErrBusy
	}
	return t.append(threadEvent{Type: evReset})
}

// turnText is a message as the PM is shown it later: its own words, then
// what it proposed and what the user did with each proposal.
func turnText(m Message) string {
	var b strings.Builder
	b.WriteString(m.Text)
	for i, p := range m.Proposals {
		fate := p.State
		if p.State == "confirmed" {
			fate = "confirmed as " + p.TaskID
		}
		fmt.Fprintf(&b, "\n[proposal %d] %s — %s", i+1, p.Title, fate)
	}
	for _, q := range m.Questions {
		b.WriteString("\n[question] " + q)
	}
	return strings.TrimSpace(b.String())
}

// History is the last max turns of the conversation, for the PM's context.
// Failed answers are left out: they say nothing the PM should build on.
func (t *Thread) History(max int) []Turn {
	t.mu.Lock()
	defer t.mu.Unlock()
	var out []Turn
	for _, m := range t.view().Messages {
		if m.Failed {
			continue
		}
		out = append(out, Turn{Role: m.Role, Text: turnText(m)})
	}
	if len(out) > max {
		out = out[len(out)-max:]
	}
	return out
}

// Advisory is the reviewer's note for key, if one was ever asked for.
func (t *Thread) Advisory(key string) (AdvisoryView, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	a, ok := t.advisories()[key]
	return a, ok
}

// AdvisoryStart marks a note for key as being written.
func (t *Thread) AdvisoryStart(key string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if a, ok := t.advisories()[key]; ok && a.State == "running" {
		return ErrBusy
	}
	return t.append(threadEvent{Type: evAdviceStart, Key: key})
}

// AdvisoryDone stores the note for key.
func (t *Thread) AdvisoryDone(key string, adv Advice, call Call) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.append(threadEvent{Type: evAdviceDone, Key: key, Advice: &adv, Call: &call})
}

// AdvisoryFail records that no note could be written for key, and why.
func (t *Thread) AdvisoryFail(key, reason string, call Call) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.append(threadEvent{Type: evAdviceFail, Key: key, Text: reason, Call: &call})
}
