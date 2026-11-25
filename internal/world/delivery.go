// internal/world/delivery.go
//
// Whether a message typed into an agent actually reached it.
//
// tmux exiting 0 only says the keys were pressed. They can land in a prompt
// the CLI never submits, in a dialog, or in a pane that changed between the
// check and the paste. The one place that says the agent received the text is
// its own transcript, so that is what a send is checked against.
package world

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"time"
)

const (
	// DeliveryAcked: the transcript shows the message as a user turn after it
	// was sent.
	DeliveryAcked = "acked"
	// DeliveryUnknown: no such turn appeared in time. It may still arrive, or
	// it may not have; nothing is claimed either way.
	DeliveryUnknown = "unknown"
	// DeliveryHeld: the agent is mid-task and its CLI put the message in its
	// own queue; it reads it when it gets there.
	DeliveryHeld = "held"
	// DeliveryCommand: a slash command, which leaves no user turn to look for.
	DeliveryCommand = "command"

	deliveryPolls    = 20
	deliveryInterval = 400 * time.Millisecond
	// transcript timestamps come from the CLI's clock, the send time from
	// ours; allow for a little disagreement.
	deliverySkew = 2 * time.Second
)

// AwaitDelivery watches a session's transcript for the message sent at
// sentAt and reports what it shows.
func (c *Collector) AwaitDelivery(ctx context.Context, id, message string, sentAt time.Time) string {
	want := deliveryKey(message)
	if want == "" {
		return DeliveryUnknown
	}
	if slashCommandRE.MatchString(message) {
		return DeliveryCommand
	}
	// Find the transcript once. Looking it up builds a snapshot of the whole
	// machine; doing that on every poll made each unacked send rescan
	// everything twenty times.
	d, err := c.Detail(ctx, id)
	if err != nil || d.Session.sourcePath == "" {
		return DeliveryUnknown
	}
	path, runtime := d.Session.sourcePath, d.Session.RuntimeType
	for i := 0; i < deliveryPolls; i++ {
		if ctx.Err() != nil {
			break
		}
		for _, m := range transcriptMessages(path, runtime, 10) {
			if m.Role == "user" && !m.At.Before(sentAt.Add(-deliverySkew)) && sameMessage(deliveryKey(m.Text), want) {
				return DeliveryAcked
			}
		}
		c.sleep(deliveryInterval)
	}
	if queuedInCLI(path, runtime, want, sentAt) {
		return DeliveryHeld
	}
	return DeliveryUnknown
}

var slashCommandRE = regexp.MustCompile(`^/[a-z][a-z0-9:-]*(\s|$)`)

// queuedInCLI reports that Claude Code recorded taking the message into its
// own queue.
func queuedInCLI(path, runtime, want string, sentAt time.Time) bool {
	fi, err := os.Stat(path)
	if runtime != "claudeCode" || err != nil {
		return false
	}
	for _, raw := range readTail(path, fi.Size(), 128<<10) {
		var ln struct {
			Type      string `json:"type"`
			Operation string `json:"operation"`
			Timestamp string `json:"timestamp"`
			Content   string `json:"content"`
		}
		if !bytes.Contains(raw, []byte(`"queue-operation"`)) || json.Unmarshal(raw, &ln) != nil || ln.Type != "queue-operation" || ln.Operation != "enqueue" {
			continue
		}
		if !parseTS(ln.Timestamp).Before(sentAt.Add(-deliverySkew)) && sameMessage(deliveryKey(cleanTurn(ln.Content)), want) {
			return true
		}
	}
	return false
}

// workspaceSession is the live session a workspace's agent window runs.
func (c *Collector) workspaceSession(ctx context.Context, workspace string) string {
	for _, ss := range c.Snapshot(ctx).Sessions {
		if ss.Live && ss.DispatchVia == workspace {
			return ss.SessionID
		}
	}
	return ""
}

// deliveryKey puts sent text in the form a transcript turn is read back in —
// redacted, whitespace collapsed — so the two can be compared.
func deliveryKey(s string) string {
	return strings.Join(strings.Fields(Redact(s)), " ")
}

// sameMessage allows for a transcript turn that was truncated for display.
func sameMessage(got, want string) bool {
	if got == want {
		return true
	}
	got = strings.TrimSuffix(got, "…")
	return len(got) >= 40 && strings.HasPrefix(want, got)
}
