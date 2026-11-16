package planner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// ErrIncomplete means the model answered, but never with something that
// passed the check, even after being told why. The caller shows that to the
// user as an incomplete answer; nothing is patched up on the model's behalf.
var ErrIncomplete = errors.New("the PM's answer did not pass the check")

// maxTries is the first try plus two retries.
const maxTries = 3

// Exec starts a program in dir with stdin and returns its stdout.
type Exec interface {
	Run(ctx context.Context, dir string, stdin []byte, name string, args ...string) ([]byte, error)
}

// Call is the record of one skill run, kept beside the answer it produced.
type Call struct {
	Adapter string `json:"adapter"`
	Skill   string `json:"skill"`
	Version string `json:"version"`
	Tries   int    `json:"tries"`
	Millis  int64  `json:"millis"`
	Outcome string `json:"outcome"` // ok | escalated | failed
}

// Cloud runs the PM's skills through the Claude Code CLI in headless mode,
// signed in with the machine's subscription.
//
// The model gets no tools, no MCP servers and no project or user settings,
// and is started in an empty directory: it can read the prompt and write an
// answer, nothing else. Its answer is constrained by a JSON Schema and then
// checked again here.
type Cloud struct {
	Exec    Exec
	Path    string        // the claude binary
	Timeout time.Duration // one try; 3 minutes if zero
}

// Name is how the UI labels this adapter.
func (c *Cloud) Name() string { return "cloud · claude" }

// Plan asks for a plan for in.Request.
func (c *Cloud) Plan(ctx context.Context, in PlanInput) (Plan, Call, error) {
	var plan Plan
	rec, err := c.run(ctx, planProject, in, func(raw json.RawMessage) error {
		plan = Plan{}
		if err := json.Unmarshal(raw, &plan); err != nil {
			return fmt.Errorf("the answer does not match the schema: %v", err)
		}
		return plan.Check(in)
	})
	if err != nil {
		return Plan{}, rec, err
	}
	return plan, rec, nil
}

// Advise asks for a reviewer's note on one attempt.
func (c *Cloud) Advise(ctx context.Context, in ReviewInput) (Advice, Call, error) {
	var adv Advice
	rec, err := c.run(ctx, summarizeReview, in, func(raw json.RawMessage) error {
		adv = Advice{}
		if err := json.Unmarshal(raw, &adv); err != nil {
			return fmt.Errorf("the answer does not match the schema: %v", err)
		}
		return adv.Check()
	})
	if err != nil {
		return Advice{}, rec, err
	}
	return adv, rec, nil
}

func (c *Cloud) run(ctx context.Context, sk skill, input any, accept func(json.RawMessage) error) (rec Call, err error) {
	rec = Call{Adapter: "cloud", Skill: sk.name, Version: sk.version}
	start := time.Now()
	defer func() { rec.Millis = time.Since(start).Milliseconds() }()
	refused := ""
	for rec.Tries < maxTries {
		rec.Tries++
		raw, err := c.once(ctx, sk, input, refused)
		if err != nil {
			rec.Outcome = "failed"
			return rec, err
		}
		if raw == nil {
			refused = "the answer was not the JSON object the schema asks for"
			continue
		}
		if err := accept(raw); err != nil {
			refused = err.Error()
			continue
		}
		rec.Outcome = "ok"
		return rec, nil
	}
	rec.Outcome = "escalated"
	return rec, fmt.Errorf("%w after %d tries: %s", ErrIncomplete, rec.Tries, refused)
}

// envelope is what `claude -p --output-format json` prints.
type envelope struct {
	Type       string          `json:"type"`
	Subtype    string          `json:"subtype"`
	IsError    bool            `json:"is_error"`
	Result     string          `json:"result"`
	Structured json.RawMessage `json:"structured_output"`
}

func parseEnvelope(out []byte) (envelope, bool) {
	out = bytes.TrimSpace(out)
	var env envelope
	if len(out) > 0 && out[0] == '[' {
		// Some builds print every message; the result is the last one.
		var all []envelope
		if json.Unmarshal(out, &all) != nil {
			return env, false
		}
		for _, e := range all {
			if e.Type == "result" {
				env = e
			}
		}
		return env, env.Type == "result"
	}
	if json.Unmarshal(out, &env) != nil || env.Type != "result" {
		return env, false
	}
	return env, true
}

func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

// once runs the CLI one time. A nil answer with a nil error means the model
// replied but not in the asked shape, which is worth one more try; an error
// means the CLI itself failed, which is not.
func (c *Cloud) once(ctx context.Context, sk skill, input any, refused string) (json.RawMessage, error) {
	prompt := obj{"input": input}
	if refused != "" {
		prompt["previousError"] = refused
	}
	stdin, err := json.Marshal(prompt)
	if err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp("", "aiagent-pm-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)

	timeout := c.Timeout
	if timeout <= 0 {
		timeout = 3 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	out, err := c.Exec.Run(ctx, dir, stdin, c.Path,
		"-p",
		"--output-format", "json",
		"--json-schema", sk.schema,
		"--tools", "",
		"--strict-mcp-config",
		"--setting-sources", "",
		"--no-session-persistence",
		"--permission-prompts", "none",
		"--system-prompt", sk.system,
	)
	env, ok := parseEnvelope(out)
	switch {
	case err != nil && ok && env.Result != "":
		return nil, fmt.Errorf("the claude CLI failed: %s", clip(env.Result, 300))
	case err != nil:
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("the claude CLI did not answer within %s", timeout)
		}
		return nil, fmt.Errorf("the claude CLI failed: %s", clip(err.Error(), 300))
	case !ok:
		return nil, fmt.Errorf("the claude CLI did not return a result: %s", clip(string(out), 200))
	case env.IsError:
		return nil, fmt.Errorf("the claude CLI reported an error (%s): %s", env.Subtype, clip(env.Result, 300))
	}
	if len(env.Structured) > 0 && string(env.Structured) != "null" {
		return env.Structured, nil
	}
	if text := strings.TrimSpace(env.Result); json.Valid([]byte(text)) && strings.HasPrefix(text, "{") {
		return json.RawMessage(text), nil
	}
	return nil, nil
}

// subscriptionEnv is the environment the CLI is started with: the service's
// own, minus anything that would switch the CLI from the signed-in
// subscription to a metered provider API.
func subscriptionEnv(environ []string) []string {
	out := make([]string, 0, len(environ))
	for _, kv := range environ {
		if strings.HasPrefix(kv, "ANTHROPIC_") || strings.HasPrefix(kv, "CLAUDE_CODE_USE_") {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// capped keeps the first max bytes written to it and drops the rest.
type capped struct {
	buf bytes.Buffer
	max int
}

func (w *capped) Write(p []byte) (int, error) {
	if room := w.max - w.buf.Len(); room > 0 {
		w.buf.Write(p[:min(room, len(p))])
	}
	return len(p), nil
}

// OSExec runs the real binary: no shell, a fixed argv, bounded output.
type OSExec struct{}

// Run implements Exec.
func (OSExec) Run(ctx context.Context, dir string, stdin []byte, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Stdin = bytes.NewReader(stdin)
	cmd.Env = subscriptionEnv(os.Environ())
	cmd.WaitDelay = 5 * time.Second
	stdout, stderr := &capped{max: 4 << 20}, &capped{max: 4 << 10}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	if err := cmd.Run(); err != nil {
		if line, _, _ := strings.Cut(strings.TrimSpace(stderr.buf.String()), "\n"); line != "" {
			return stdout.buf.Bytes(), fmt.Errorf("%w: %s", err, line)
		}
		return stdout.buf.Bytes(), err
	}
	return stdout.buf.Bytes(), nil
}
