package planner

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func openThread(t *testing.T) (*Thread, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "pm.jsonl")
	th, err := OpenThread(path)
	if err != nil {
		t.Fatalf("OpenThread: %v", err)
	}
	return th, path
}

func checkedPlan(t *testing.T) Plan {
	t.Helper()
	p := goodPlan()
	if err := p.Check(planContext); err != nil {
		t.Fatal(err)
	}
	return p
}

// One question at a time: while the PM is thinking, a second message is
// refused instead of queued behind an answer the user has not read.
func TestThreadAskThenReply(t *testing.T) {
	th, _ := openThread(t)
	if _, err := th.Ask("ช่วยวางแผนหน่อย"); err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if !th.View().Thinking {
		t.Fatal("the thread is not thinking after a question")
	}
	if _, err := th.Ask("อีกเรื่อง"); !errors.Is(err, ErrBusy) {
		t.Fatalf("a second question while thinking: err = %v, want ErrBusy", err)
	}
	if err := th.Reply(checkedPlan(t), Call{Skill: "planProject", Version: "v1", Tries: 1, Outcome: "ok"}); err != nil {
		t.Fatalf("Reply: %v", err)
	}

	v := th.View()
	if v.Thinking || len(v.Messages) != 2 {
		t.Fatalf("view = thinking %v, %d messages; want idle and 2", v.Thinking, len(v.Messages))
	}
	user, pm := v.Messages[0], v.Messages[1]
	if user.Role != "user" || user.Text != "ช่วยวางแผนหน่อย" {
		t.Errorf("first message = %+v", user)
	}
	if pm.Role != "pm" || pm.Text != "เสนอเป็น 2 งาน" || pm.Call == nil || pm.Call.Skill != "planProject" {
		t.Errorf("reply = %+v", pm)
	}
	if len(pm.Proposals) != 2 || pm.Proposals[0].State != "open" || pm.Proposals[0].ID == pm.Proposals[1].ID || pm.Proposals[0].ID == "" {
		t.Fatalf("proposals = %+v", pm.Proposals)
	}
	if err := th.Reply(checkedPlan(t), Call{}); err == nil {
		t.Error("a reply nobody asked for was accepted")
	}
}

// The PM's next answer builds on what it proposed and what became of it, so
// it does not propose the same task twice or forget what was turned down.
func TestThreadHistoryCarriesProposalsAndTheirFate(t *testing.T) {
	th, _ := openThread(t)
	th.Ask("แผน")
	th.Reply(checkedPlan(t), Call{})
	ps := th.View().Messages[1].Proposals
	if err := th.Confirm(ps[0].ID, "T-0007"); err != nil {
		t.Fatal(err)
	}
	if err := th.Dismiss(ps[1].ID); err != nil {
		t.Fatal(err)
	}
	h := th.History(10)
	if len(h) != 2 || h[1].Role != "pm" {
		t.Fatalf("history = %+v", h)
	}
	for _, want := range []string{"เสนอเป็น 2 งาน", ps[0].Title, "T-0007", ps[1].Title, "dismissed"} {
		if !strings.Contains(h[1].Text, want) {
			t.Errorf("the PM's own turn = %q, missing %q", h[1].Text, want)
		}
	}
}

// The key a confirmation is made idempotent by is the same on every retry,
// and is not shared with a proposal of another log that got the same id — a
// replaced pm.jsonl must not hand back somebody else's task.
func TestThreadConfirmKeyIsStableAndNotSharedAcrossLogs(t *testing.T) {
	keyOf := func() (string, string) {
		th, _ := openThread(t)
		th.Ask("แผน")
		th.Reply(checkedPlan(t), Call{})
		id := th.View().Messages[1].Proposals[0].ID
		k, ok := th.ConfirmKey(id)
		if again, _ := th.ConfirmKey(id); !ok || k == "" || again != k {
			t.Fatalf("ConfirmKey(%s) = %q then %q, ok %v", id, k, again, ok)
		}
		return id, k
	}
	id1, k1 := keyOf()
	id2, k2 := keyOf()
	if id1 != id2 || k1 == k2 {
		t.Errorf("two logs: ids %s and %s, keys %q and %q; want the same id and different keys", id1, id2, k1, k2)
	}
	th, _ := openThread(t)
	if _, ok := th.ConfirmKey("P1-1"); ok {
		t.Error("a key for a proposal that does not exist")
	}
}

// A proposal is decided once: confirmed with the task it became, or dismissed.
func TestThreadProposalIsDecidedOnce(t *testing.T) {
	th, _ := openThread(t)
	th.Ask("แผน")
	th.Reply(checkedPlan(t), Call{})
	ps := th.View().Messages[1].Proposals
	first, second := ps[0].ID, ps[1].ID

	if err := th.Confirm(first, "T-0001"); err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	if err := th.Confirm(first, "T-0001"); err != nil {
		t.Errorf("confirming again with the same task should be a no-op: %v", err)
	}
	if err := th.Confirm(first, "T-0002"); err == nil {
		t.Error("a confirmed proposal was confirmed as a second task")
	}
	if err := th.Dismiss(first); err == nil {
		t.Error("a confirmed proposal was dismissed")
	}
	if err := th.Dismiss(second); err != nil {
		t.Fatalf("Dismiss: %v", err)
	}
	if err := th.Confirm(second, "T-0003"); err == nil {
		t.Error("a dismissed proposal was confirmed")
	}
	if err := th.Confirm("P9-9", "T-0004"); err == nil {
		t.Error("a proposal that does not exist was confirmed")
	}

	p, siblings, ok := th.Proposal(first)
	if !ok || p.State != "confirmed" || p.TaskID != "T-0001" || len(siblings) != 2 || siblings[1].State != "dismissed" {
		t.Errorf("Proposal = %+v, siblings = %+v, ok = %v", p, siblings, ok)
	}
}

// The log is the thread: closing and opening it again shows the same thing.
func TestThreadSurvivesReopen(t *testing.T) {
	th, path := openThread(t)
	th.Ask("แผน")
	th.Reply(checkedPlan(t), Call{})
	id := th.View().Messages[1].Proposals[0].ID
	th.Confirm(id, "T-0007")

	again, err := OpenThread(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	v := again.View()
	if len(v.Messages) != 2 || v.Messages[1].Proposals[0].State != "confirmed" || v.Messages[1].Proposals[0].TaskID != "T-0007" {
		t.Fatalf("after reopen: %+v", v.Messages)
	}
}

// If the service stopped while the PM was thinking, the answer is never
// coming. The thread says so and takes the next question, instead of showing
// "thinking" forever.
func TestThreadInterruptedThinkingBecomesAFailure(t *testing.T) {
	th, path := openThread(t)
	th.Ask("แผน")

	again, err := OpenThread(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	v := again.View()
	if v.Thinking || len(v.Messages) != 2 || !v.Messages[1].Failed {
		t.Fatalf("after an interrupted question: thinking %v, messages %+v", v.Thinking, v.Messages)
	}
	if _, err := again.Ask("ลองใหม่"); err != nil {
		t.Errorf("the thread did not take a new question: %v", err)
	}
}

// A crash mid-append leaves half a line; it is cut off, not treated as damage.
func TestThreadCutsOffATornLastLine(t *testing.T) {
	th, path := openThread(t)
	th.Ask("แผน")
	th.Fail("the claude CLI failed", Call{Outcome: "failed"})
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	f.WriteString(`{"seq":3,"type":"userMess`)
	f.Close()

	again, err := OpenThread(path)
	if err != nil {
		t.Fatalf("reopen with a torn line: %v", err)
	}
	if _, err := again.Ask("ต่อ"); err != nil {
		t.Fatalf("Ask after a torn line: %v", err)
	}
	third, err := OpenThread(path)
	if err != nil || len(third.View().Messages) != 4 {
		t.Fatalf("the log after the torn line is not readable: %v", err)
	}
}

// Starting over hides the old conversation from the view and from what the
// PM is shown next; the log itself keeps it.
func TestThreadResetStartsANewConversation(t *testing.T) {
	th, _ := openThread(t)
	th.Ask("เรื่องเก่า")
	th.Reply(Plan{Reply: "รับทราบ"}, Call{})
	if got := th.History(10); len(got) != 2 || got[0].Role != "user" || got[1].Text != "รับทราบ" {
		t.Fatalf("history = %+v", got)
	}
	if err := th.Reset(); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if v := th.View(); len(v.Messages) != 0 || len(th.History(10)) != 0 {
		t.Fatalf("after reset: %d messages, history %d", len(v.Messages), len(th.History(10)))
	}
}

// Advice is kept per attempt, and one request at a time per attempt.
func TestThreadKeepsAdvicePerAttempt(t *testing.T) {
	th, path := openThread(t)
	if _, ok := th.Advisory("T-0001-A1"); ok {
		t.Fatal("advice exists before anyone asked")
	}
	if err := th.AdvisoryStart("T-0001-A1"); err != nil {
		t.Fatalf("AdvisoryStart: %v", err)
	}
	if err := th.AdvisoryStart("T-0001-A1"); !errors.Is(err, ErrBusy) {
		t.Errorf("a second request while one runs: err = %v, want ErrBusy", err)
	}
	if a, _ := th.Advisory("T-0001-A1"); a.State != "running" {
		t.Errorf("state = %q, want running", a.State)
	}
	th.AdvisoryDone("T-0001-A1", Advice{Summary: "สรุป"}, Call{Skill: "summarizeReview"})
	if a, _ := th.Advisory("T-0001-A1"); a.State != "done" || a.Advice == nil || a.Advice.Summary != "สรุป" {
		t.Errorf("advice = %+v", a)
	}

	th.AdvisoryStart("T-0002-A1")
	again, _ := OpenThread(path)
	if a, _ := again.Advisory("T-0002-A1"); a.State != "failed" {
		t.Errorf("an interrupted request reopened as %q, want failed", a.State)
	}
	if a, _ := again.Advisory("T-0001-A1"); a.State != "done" {
		t.Errorf("finished advice was lost on reopen: %+v", a)
	}
}
