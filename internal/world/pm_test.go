package world

import (
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hotkhwan/aiagent/internal/planner"
	"github.com/hotkhwan/aiagent/internal/taskstore"
)

func twoStepPlan() planner.Plan {
	return planner.Plan{
		Reply: "เสนอเป็น 2 งาน",
		Proposals: []planner.Proposal{
			{Title: "แก้ outbox ไม่ให้ส่งซ้ำ", ProjectID: "api", Kind: "bug", Provider: "claude", AcceptanceCriteria: []string{"restart แล้วไม่ส่งซ้ำ"}, RiskLabels: []string{"deploy"}},
			{Title: "ยืนยันการรับข้อความ", ProjectID: "api", Kind: "feature", Provider: "auto", AcceptanceCriteria: []string{"บันทึก ack"}, DependsOn: []int{1}},
		},
	}
}

func pmEnv(t *testing.T, pm *fakePM) (*env, string) {
	t.Helper()
	e, tok := taskEnv(t)
	withPM(t, e, pm)
	return e, tok
}

// ask sends one message and waits for the PM's background answer.
func ask(t *testing.T, e *env, tok, text string) {
	t.Helper()
	code, m, _ := e.do(t, "POST", "/aiagent/api/pm/messages", tok, `{"text":"`+text+`"}`)
	if code != 200 {
		t.Fatalf("message: %d %v", code, m)
	}
	e.svc.bg.Wait()
	time.Sleep(writeSpacing)
}

func pmView(t *testing.T, e *env, tok string) map[string]any {
	t.Helper()
	code, m, _ := e.do(t, "GET", "/aiagent/api/pm", tok, "")
	if code != 200 {
		t.Fatalf("pm: %d %v", code, m)
	}
	return m["details"].(map[string]any)
}

func proposalsOf(t *testing.T, view map[string]any, message int) []map[string]any {
	t.Helper()
	msgs, _ := view["messages"].([]any)
	if message >= len(msgs) {
		t.Fatalf("no message %d in %v", message, msgs)
	}
	var out []map[string]any
	ps, _ := msgs[message].(map[string]any)["proposals"].([]any)
	for _, p := range ps {
		out = append(out, p.(map[string]any))
	}
	return out
}

func TestPMIsAbsentUntilConfigured(t *testing.T) {
	e, tok := taskEnv(t)
	for _, r := range [][2]string{{"GET", "/aiagent/api/pm"}, {"POST", "/aiagent/api/pm/messages"}, {"POST", "/aiagent/api/pm/reset"}} {
		if code, _, _ := e.do(t, r[0], r[1], tok, `{"text":"x"}`); code != 404 {
			t.Errorf("%s %s without a PM: %d, want 404", r[0], r[1], code)
		}
	}
}

// The PM answers with proposals and that is all it does: it is shown the
// projects and tasks that exist, and nothing is created by its answer.
func TestPMAnswersWithProposalsAndCreatesNothing(t *testing.T) {
	pm := &fakePM{plan: twoStepPlan()}
	e, tok := pmEnv(t, pm)
	if _, err := e.svc.Tasks.Create(taskstore.NewTask{ProjectID: "legacy", Title: "งานเดิม", AcceptanceCriteria: []string{"x"}}, taskstore.ActorUser, ""); err != nil {
		t.Fatal(err)
	}

	ask(t, e, tok, "อยากให้ส่งข้อความถึง agent ได้แน่นอน token=abcdefghijklmnop")

	if !strings.Contains(pm.planIn.Request, "ส่งข้อความถึง agent") || strings.Contains(pm.planIn.Request, "abcdefghijklmnop") {
		t.Errorf("the PM was sent %q; want the request with the secret removed", pm.planIn.Request)
	}
	for _, want := range []string{"api", "web", "docs", "legacy"} {
		if !slices.Contains(pm.planIn.Projects, want) {
			t.Errorf("projects shown to the PM = %v, missing %s", pm.planIn.Projects, want)
		}
	}
	if len(pm.planIn.Tasks) != 1 || pm.planIn.Tasks[0].ID != "T-0001" || len(pm.planIn.Agents) != 3 {
		t.Errorf("context shown to the PM: tasks %v, agents %v", pm.planIn.Tasks, pm.planIn.Agents)
	}

	v := pmView(t, e, tok)
	if v["adapter"] != "fake" || v["thinking"] != false {
		t.Errorf("view = adapter %v, thinking %v", v["adapter"], v["thinking"])
	}
	msgs, _ := v["messages"].([]any)
	if len(msgs) != 2 || strings.Contains(msgs[0].(map[string]any)["text"].(string), "abcdefghijklmnop") {
		t.Fatalf("messages = %v", msgs)
	}
	ps := proposalsOf(t, v, 1)
	if len(ps) != 2 || ps[0]["state"] != "open" || ps[1]["provider"] != "" {
		t.Fatalf("proposals = %v", ps)
	}
	if n := len(e.svc.Tasks.List()); n != 1 {
		t.Fatalf("the PM's answer created tasks: %d in the store, want the 1 that was there", n)
	}
	b, _ := os.ReadFile(e.auditFP)
	if !strings.Contains(string(b), `"rpc":"aiagent.pmMessage"`) {
		t.Error("a message to the PM must be audited")
	}
}

// A proposal becomes a task only when the user confirms it — as the user's
// task, once, with its place in the plan kept as a dependency.
func TestPMProposalBecomesATaskOnlyWhenConfirmed(t *testing.T) {
	e, tok := pmEnv(t, &fakePM{plan: twoStepPlan()})
	ask(t, e, tok, "แผน")
	ps := proposalsOf(t, pmView(t, e, tok), 1)
	first, second := ps[0]["id"].(string), ps[1]["id"].(string)

	code, m, _ := e.do(t, "POST", "/aiagent/api/pm/proposals/"+second+"/confirm", tok, "{}")
	if code != 409 {
		t.Fatalf("confirming a step before the one it depends on: %d %v, want 409", code, m)
	}
	time.Sleep(writeSpacing)

	code, m, _ = e.do(t, "POST", "/aiagent/api/pm/proposals/"+first+"/confirm", tok, "{}")
	if code != 200 {
		t.Fatalf("confirm: %d %v", code, m)
	}
	task, _ := m["details"].(map[string]any)["task"].(map[string]any)
	if task["taskId"] != "T-0001" || task["createdBy"] != "user" || task["status"] != "queued" || task["title"] != "แก้ outbox ไม่ให้ส่งซ้ำ" || task["provider"] != "claude" {
		t.Fatalf("task = %v", task)
	}
	time.Sleep(writeSpacing)

	code, m, _ = e.do(t, "POST", "/aiagent/api/pm/proposals/"+first+"/confirm", tok, "{}")
	if code != 200 || len(e.svc.Tasks.List()) != 1 {
		t.Fatalf("confirming twice: %d, %d tasks; want 200 and still 1", code, len(e.svc.Tasks.List()))
	}
	time.Sleep(writeSpacing)

	code, m, _ = e.do(t, "POST", "/aiagent/api/pm/proposals/"+second+"/confirm", tok,
		`{"title":"ยืนยันการรับ (แก้ชื่อ)","acceptanceCriteria":["บันทึก ack","มี test"],"provider":"codex"}`)
	if code != 200 {
		t.Fatalf("confirm with edits: %d %v", code, m)
	}
	got, _ := e.svc.Tasks.Get("T-0002")
	if got.Title != "ยืนยันการรับ (แก้ชื่อ)" || len(got.AcceptanceCriteria) != 2 || got.Provider != "codex" || !slices.Equal(got.BlockedBy, []string{"T-0001"}) {
		t.Fatalf("edited task = %+v; response %v; store %+v", got.NewTask, m, e.svc.Tasks.List())
	}

	ps = proposalsOf(t, pmView(t, e, tok), 1)
	if ps[0]["state"] != "confirmed" || ps[0]["taskId"] != "T-0001" || ps[1]["taskId"] != "T-0002" {
		t.Errorf("proposals after confirming = %v", ps)
	}
	b, _ := os.ReadFile(e.auditFP)
	if !strings.Contains(string(b), `"rpc":"aiagent.pmConfirm"`) {
		t.Error("confirming a proposal must be audited")
	}
}

// Dismissing a step frees the ones that waited on it; an edit that the task
// store would refuse is refused here too.
func TestPMDismissAndBadEdits(t *testing.T) {
	e, tok := pmEnv(t, &fakePM{plan: twoStepPlan()})
	ask(t, e, tok, "แผน")
	ps := proposalsOf(t, pmView(t, e, tok), 1)
	first, second := ps[0]["id"].(string), ps[1]["id"].(string)

	if code, m, _ := e.do(t, "POST", "/aiagent/api/pm/proposals/"+first+"/dismiss", tok, "{}"); code != 200 {
		t.Fatalf("dismiss: %d %v", code, m)
	}
	time.Sleep(writeSpacing)
	if code, _, _ := e.do(t, "POST", "/aiagent/api/pm/proposals/"+first+"/confirm", tok, "{}"); code != 409 {
		t.Fatalf("confirming a dismissed proposal: %d, want 409", code)
	}
	time.Sleep(writeSpacing)
	for _, bad := range []string{`{"provider":"gpt"}`, `{"kind":"epic"}`, `{"allowedPaths":["/etc/**"]}`} {
		if code, _, _ := e.do(t, "POST", "/aiagent/api/pm/proposals/"+second+"/confirm", tok, bad); code != 400 {
			t.Errorf("confirm with %s: %d, want 400", bad, code)
		}
		time.Sleep(writeSpacing)
	}
	code, m, _ := e.do(t, "POST", "/aiagent/api/pm/proposals/"+second+"/confirm", tok, "{}")
	if code != 200 {
		t.Fatalf("confirm after its dependency was dismissed: %d %v", code, m)
	}
	if got, _ := e.svc.Tasks.Get("T-0001"); len(got.BlockedBy) != 0 {
		t.Errorf("blockedBy = %v, want none", got.BlockedBy)
	}
	if code, _, _ := e.do(t, "POST", "/aiagent/api/pm/proposals/P999-1/dismiss", tok, "{}"); code == 200 {
		t.Error("a proposal that does not exist was dismissed")
	}
}

// An answer that never passed the check is shown as incomplete. Nothing is
// made up to fill it in.
func TestPMFailureIsShownAndTheThreadGoesOn(t *testing.T) {
	pm := &fakePM{err: errors.Join(planner.ErrIncomplete, errors.New("proposals[0].kind"))}
	e, tok := pmEnv(t, pm)
	ask(t, e, tok, "แผน")
	v := pmView(t, e, tok)
	msgs, _ := v["messages"].([]any)
	last, _ := msgs[len(msgs)-1].(map[string]any)
	if v["thinking"] != false || last["failed"] != true || last["proposals"] != nil {
		t.Fatalf("after a failed answer: %v", v)
	}

	pm.err, pm.plan = nil, planner.Plan{Reply: "ได้เลย"}
	ask(t, e, tok, "ลองใหม่")
	if len(pm.planIn.History) != 1 || pm.planIn.History[0].Role != "user" {
		t.Errorf("history shown to the PM = %+v; a failed answer is left out", pm.planIn.History)
	}

	if code, m, _ := e.do(t, "POST", "/aiagent/api/pm/reset", tok, "{}"); code != 200 {
		t.Fatalf("reset: %d %v", code, m)
	}
	if msgs, _ := pmView(t, e, tok)["messages"].([]any); len(msgs) != 0 {
		t.Errorf("after reset: %d messages", len(msgs))
	}
}

// The PM call runs behind the response, where nothing else would catch a
// crash: an uncaught panic there ends the whole service. It is that answer's
// failure instead, and the next message is answered as usual.
func TestAPMCallThatPanicsFailsOnlyThatAnswer(t *testing.T) {
	pm := &fakePM{panics: "index out of range [3] with length 2"}
	e, tok := pmEnv(t, pm)
	ask(t, e, tok, "แผน")
	v := pmView(t, e, tok)
	msgs, _ := v["messages"].([]any)
	last, _ := msgs[len(msgs)-1].(map[string]any)
	if v["thinking"] != false || last["failed"] != true {
		t.Fatalf("after a call that crashed: %v", v)
	}
	if text, _ := last["text"].(string); text == "" || strings.Contains(text, "index out of range") {
		t.Errorf("text shown = %q; it should say the call crashed without the panic text", text)
	}

	pm.panics, pm.plan = "", planner.Plan{Reply: "ได้เลย"}
	ask(t, e, tok, "ลองใหม่")
	msgs, _ = pmView(t, e, tok)["messages"].([]any)
	if last, _ := msgs[len(msgs)-1].(map[string]any); last["failed"] == true || last["text"] != "ได้เลย" {
		t.Errorf("the message after the crash was answered with %v", last)
	}
}

// The PM is told which agents could take work right now. "Nothing configured"
// and "configured but not running" are different answers, and neither is ready.
func TestAgentStatesTellNotConfiguredFromNotReady(t *testing.T) {
	ws := []workspaceEntry{
		{name: "a", session: "a", provider: "claude"},
		{name: "b", session: "b", provider: "claude"},
		{name: "c", session: "c", provider: "codex"},
		{name: "plain", session: "plain"},
	}
	got := agentStates(ws, func(w workspaceEntry) bool { return w.name == "b" || w.name == "plain" })
	want := []planner.AgentBrief{{Provider: "claude", State: "ready"}, {Provider: "codex", State: "notReady"}, {Provider: "gemini", State: "notConfigured"}}
	if !slices.Equal(got, want) {
		t.Errorf("agentStates = %+v, want %+v", got, want)
	}
}

// One question at a time, and empty or oversized text never reaches the model.
func TestPMRefusesWhatItShouldNotTake(t *testing.T) {
	pm := &fakePM{plan: planner.Plan{Reply: "ok"}, block: make(chan struct{})}
	e, tok := pmEnv(t, pm)
	for _, body := range []string{`{"text":"   "}`, `{"text":"` + strings.Repeat("ก", 4001) + `"}`, `not json`} {
		if code, _, _ := e.do(t, "POST", "/aiagent/api/pm/messages", tok, body); code != 400 {
			t.Errorf("message %.20q: %d, want 400", body, code)
		}
	}
	if code, m, _ := e.do(t, "POST", "/aiagent/api/pm/messages", tok, `{"text":"แรก"}`); code != 200 {
		t.Fatalf("message: %d %v", code, m)
	}
	time.Sleep(writeSpacing)
	if v := pmView(t, e, tok); v["thinking"] != true {
		t.Errorf("the PM should be thinking: %v", v)
	}
	if code, _, _ := e.do(t, "POST", "/aiagent/api/pm/messages", tok, `{"text":"สอง"}`); code != 409 {
		t.Errorf("a second message while thinking: %d, want 409", code)
	}
	close(pm.block)
	e.svc.bg.Wait()
	if code, _, _ := e.do(t, "GET", "/aiagent/api/pm", "", ""); code == 200 {
		t.Error("the PM thread was readable without a token")
	}
}
