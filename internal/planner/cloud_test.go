package planner

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
)

type call struct {
	dir   string
	stdin string
	name  string
	args  []string
	// what the working directory held when the CLI was started
	entries int
	dirErr  error
}

// fakeExec stands in for the claude binary: it records how it was started and
// plays back one canned stdout per call.
type fakeExec struct {
	calls []call
	outs  []string
	err   error
}

func (f *fakeExec) Run(_ context.Context, dir string, stdin []byte, name string, args ...string) ([]byte, error) {
	c := call{dir: dir, stdin: string(stdin), name: name, args: args}
	ents, err := os.ReadDir(dir)
	c.entries, c.dirErr = len(ents), err
	f.calls = append(f.calls, c)
	if f.err != nil {
		return []byte("boom"), f.err
	}
	out := f.outs[min(len(f.calls)-1, len(f.outs)-1)]
	return []byte(out), nil
}

func envelopeOf(t *testing.T, structured any) string {
	t.Helper()
	b, err := json.Marshal(map[string]any{"type": "result", "subtype": "success", "is_error": false, "structured_output": structured})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func argAfter(args []string, flag string) (string, bool) {
	i := slices.Index(args, flag)
	if i < 0 || i+1 >= len(args) {
		return "", false
	}
	return args[i+1], true
}

// The PM is a model with no hands: it is started headless with every tool,
// MCP server and project setting switched off, in a directory with nothing in
// it, and it gets the request on stdin where it cannot be read as a flag.
func TestCloudPlanStartsClaudeWithNoToolsInAnEmptyDirectory(t *testing.T) {
	fx := &fakeExec{outs: []string{envelopeOf(t, goodPlan())}}
	c := &Cloud{Exec: fx, Path: "/opt/bin/claude"}

	plan, rec, err := c.Plan(context.Background(), planContext)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(plan.Proposals) != 2 || plan.Proposals[0].Kind != "bug" {
		t.Fatalf("plan was not carried through: %+v", plan)
	}
	if rec.Skill != "planProject" || rec.Version != "v1" || rec.Tries != 1 || rec.Outcome != "ok" || rec.Adapter != "cloud" {
		t.Errorf("call record = %+v", rec)
	}

	got := fx.calls[0]
	if got.name != "/opt/bin/claude" {
		t.Errorf("ran %q", got.name)
	}
	for _, flag := range []string{"-p", "--strict-mcp-config", "--no-session-persistence"} {
		if !slices.Contains(got.args, flag) {
			t.Errorf("argv is missing %s: %q", flag, got.args)
		}
	}
	for flag, want := range map[string]string{"--tools": "", "--setting-sources": "", "--output-format": "json", "--permission-prompts": "none"} {
		if v, ok := argAfter(got.args, flag); !ok || v != want {
			t.Errorf("%s = %q (present %v), want %q", flag, v, ok, want)
		}
	}
	if schema, _ := argAfter(got.args, "--json-schema"); !json.Valid([]byte(schema)) {
		t.Errorf("--json-schema is not JSON: %q", schema)
	}
	if sys, _ := argAfter(got.args, "--system-prompt"); !strings.Contains(sys, "never act") {
		t.Errorf("the system prompt does not tell the PM it cannot act")
	}
	for _, a := range got.args {
		if strings.Contains(a, planContext.Request) {
			t.Errorf("the request reached argv; it belongs on stdin")
		}
	}
	if !strings.Contains(got.stdin, planContext.Request) {
		t.Errorf("stdin does not carry the request: %q", got.stdin)
	}
	if got.dirErr != nil || got.entries != 0 {
		t.Errorf("working directory: err=%v, %d entries; want an existing empty one", got.dirErr, got.entries)
	}
	if _, err := os.Stat(got.dir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the working directory was left behind: %v", err)
	}
}

// A plan that fails the check goes back to the model with the reason, twice
// at most.
func TestCloudPlanRetriesWithTheReasonItWasRefused(t *testing.T) {
	bad := goodPlan()
	bad.Proposals[0].Kind = "epic"
	fx := &fakeExec{outs: []string{envelopeOf(t, bad), envelopeOf(t, goodPlan())}}
	c := &Cloud{Exec: fx, Path: "claude"}

	_, rec, err := c.Plan(context.Background(), planContext)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if rec.Tries != 2 || len(fx.calls) != 2 {
		t.Fatalf("tries = %d, calls = %d; want 2", rec.Tries, len(fx.calls))
	}
	if strings.Contains(fx.calls[0].stdin, "previousError") {
		t.Errorf("the first try already carried an error")
	}
	if !strings.Contains(fx.calls[1].stdin, "previousError") || !strings.Contains(fx.calls[1].stdin, "epic") {
		t.Errorf("the retry was not told why: %q", fx.calls[1].stdin)
	}
}

// After two retries the PM stops and says so: an incomplete answer is shown
// to the user as incomplete, never patched up.
func TestCloudPlanGivesUpAfterTwoRetries(t *testing.T) {
	bad := goodPlan()
	bad.Proposals[0].AcceptanceCriteria = nil
	fx := &fakeExec{outs: []string{envelopeOf(t, bad)}}
	c := &Cloud{Exec: fx, Path: "claude"}

	_, rec, err := c.Plan(context.Background(), planContext)
	if !errors.Is(err, ErrIncomplete) {
		t.Fatalf("err = %v, want ErrIncomplete", err)
	}
	if len(fx.calls) != 3 || rec.Tries != 3 || rec.Outcome != "escalated" {
		t.Errorf("calls = %d, record = %+v; want 3 tries and escalated", len(fx.calls), rec)
	}
}

// A CLI that will not start, or reports an error of its own (not signed in,
// out of quota), is not asked again: asking again would spend the same quota
// on the same failure.
func TestCloudPlanDoesNotRetryAFailedCLI(t *testing.T) {
	for name, fx := range map[string]*fakeExec{
		"exec failed":     {err: errors.New("exit status 1")},
		"cli says error":  {outs: []string{`{"type":"result","subtype":"error_during_execution","is_error":true,"result":"Not logged in"}`}},
		"not json at all": {outs: []string{"Please run /login"}},
	} {
		t.Run(name, func(t *testing.T) {
			c := &Cloud{Exec: fx, Path: "claude"}
			_, rec, err := c.Plan(context.Background(), planContext)
			if err == nil || errors.Is(err, ErrIncomplete) {
				t.Fatalf("err = %v, want a CLI failure", err)
			}
			if len(fx.calls) != 1 || rec.Outcome != "failed" {
				t.Errorf("calls = %d, outcome = %q; want one call and failed", len(fx.calls), rec.Outcome)
			}
		})
	}
}

// Older builds return the object as text in result instead of structured_output.
func TestCloudPlanReadsAnAnswerReturnedAsText(t *testing.T) {
	inner, _ := json.Marshal(goodPlan())
	out, _ := json.Marshal(map[string]any{"type": "result", "subtype": "success", "result": string(inner)})
	fx := &fakeExec{outs: []string{string(out)}}
	c := &Cloud{Exec: fx, Path: "claude"}
	plan, _, err := c.Plan(context.Background(), planContext)
	if err != nil || len(plan.Proposals) != 2 {
		t.Fatalf("plan = %+v, err = %v", plan, err)
	}
}

func TestCloudAdviseUsesTheReviewSkill(t *testing.T) {
	fx := &fakeExec{outs: []string{envelopeOf(t, Advice{Summary: "เพิ่ม ack", Concerns: []Concern{{Severity: "info", Note: "ดู test เพิ่ม"}}})}}
	c := &Cloud{Exec: fx, Path: "claude"}
	adv, rec, err := c.Advise(context.Background(), ReviewInput{TaskID: "T-0001", Title: "ack", Diff: "+x"})
	if err != nil {
		t.Fatalf("Advise: %v", err)
	}
	if adv.Summary != "เพิ่ม ack" || rec.Skill != "summarizeReview" {
		t.Errorf("advice = %+v, record = %+v", adv, rec)
	}
	if sys, _ := argAfter(fx.calls[0].args, "--system-prompt"); !strings.Contains(sys, "not the judge") {
		t.Errorf("the reviewer is not told it is advisory")
	}
}

// The subscription login is the only way in: a provider API key in the
// service's environment must not reach the CLI, or it would bill that key.
func TestSubscriptionEnvDropsProviderKeys(t *testing.T) {
	got := subscriptionEnv([]string{"HOME=/Users/x", "ANTHROPIC_API_KEY=k", "ANTHROPIC_AUTH_TOKEN=t", "ANTHROPIC_BASE_URL=u", "PATH=/bin", "CLAUDE_CODE_USE_BEDROCK=1"})
	want := []string{"HOME=/Users/x", "PATH=/bin"}
	if !slices.Equal(got, want) {
		t.Errorf("env = %q, want %q", got, want)
	}
}
