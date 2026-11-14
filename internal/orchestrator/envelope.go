package orchestrator

import (
	"fmt"
	"strings"

	"github.com/hotkhwan/aiagent/internal/taskstore"
)

// maxEnvelope is what a session accepts in one message (world's text limit),
// with room to spare.
const maxEnvelope = 3800

// Envelope is the one message an agent gets for an attempt. It is complete on
// its own — the agent needs nothing else to start — and it says how to report
// back, because the result file is how the orchestrator learns the work is
// done. The criteria are kept in the user's words.
func Envelope(t taskstore.Task, a taskstore.Attempt) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[aiagent task %s · attempt %s · revision %d]\n%s\n\n", t.ID, a.ID, t.Revision, t.Title)
	b.WriteString("Acceptance criteria — all must hold:\n")
	for _, c := range t.AcceptanceCriteria {
		fmt.Fprintf(&b, "- %s\n", c)
	}
	if len(t.AllowedPaths) > 0 {
		fmt.Fprintf(&b, "\nChange only: %s\n", strings.Join(t.AllowedPaths, ", "))
	}
	tail := fmt.Sprintf("\nStart a new branch from the base commit: git switch -c %s %s\n"+
		"Commit your work on that branch. Do not push, merge or rebase, and do not weaken or delete tests, CI or lint rules to make them pass.\n"+
		"When done, write %s as JSON: {\"status\":\"done\",\"commit\":\"<sha of your last commit>\",\"summary\":\"<what changed>\"}\n"+
		"If you cannot continue, write {\"status\":\"blocked\",\"summary\":\"<your question>\"} there instead and stop.",
		a.Branch, a.BaseCommit, ResultPath(".", a.ID))
	head := b.String()
	if room := maxEnvelope - len([]rune(tail)); len([]rune(head)) > room {
		head = string([]rune(head)[:room-2]) + "…\n"
	}
	return head + tail
}
