package worker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/hotkhwan/aiagent/internal/sandbox"
)

// ErrMissingCLI means the worker CLI is not on PATH. The orchestrator turns it
// into blocked{missingCli}, not into a crash.
var ErrMissingCLI = errors.New("worker CLI not installed")

// WorkerEnvAllow names the only variables a worker inherits. Credentials for
// GitHub, cloud providers or the API are left out on purpose: the CLI runs on
// its own subscription login.
var WorkerEnvAllow = []string{"PATH", "HOME", "USER", "LOGNAME", "LANG", "LC_ALL", "TERM", "TMPDIR", "SHELL"}

// gitIdentity lets the worker commit without touching the operator's git
// config. It is not a person's identity.
var gitIdentity = []string{
	"GIT_AUTHOR_NAME=aiagent-worker",
	"GIT_AUTHOR_EMAIL=worker@aiagent.invalid",
	"GIT_COMMITTER_NAME=aiagent-worker",
	"GIT_COMMITTER_EMAIL=worker@aiagent.invalid",
}

// Tools is what a worker may call. Claude's -p mode cannot ask, so anything
// not allowed is refused; disallowed is listed too so it stays refused if the
// allow list grows.
type Tools struct {
	Allowed    []string
	Disallowed []string
}

// DefaultTools is the profile every attempt runs with: edit and inspect the
// worktree, commit on the attempt's branch, run the Go checks. Pushing,
// merging, rebasing, resetting and changing remotes are refused.
var DefaultTools = Tools{
	Allowed: []string{
		"Read", "Edit", "Write",
		"Bash(git status:*)", "Bash(git diff:*)", "Bash(git log:*)",
		"Bash(git add:*)", "Bash(git commit:*)", "Bash(git switch:*)",
		"Bash(go test:*)", "Bash(go vet:*)", "Bash(gofmt:*)",
	},
	Disallowed: []string{
		"Bash(git push:*)", "Bash(git merge:*)", "Bash(git rebase:*)",
		"Bash(git reset:*)", "Bash(git checkout:*)", "Bash(git remote:*)",
		"Bash(git worktree:*)", "Bash(git config:*)",
	},
}

// ClaudeArgs is the argv after the binary. The prompt is not here: it is read
// from stdin so it never shows in ps or in the argv of a crash report.
func ClaudeArgs(t Tools) []string {
	return []string{
		"-p",
		"--output-format", "stream-json",
		"--verbose",
		"--allowedTools", strings.Join(t.Allowed, ","),
		"--disallowedTools", strings.Join(t.Disallowed, ","),
	}
}

// FilterEnv keeps the entries of parent whose names are in allow.
func FilterEnv(parent, allow []string) []string {
	var out []string
	for _, kv := range parent {
		name, _, _ := strings.Cut(kv, "=")
		for _, a := range allow {
			if name == a {
				out = append(out, kv)
				break
			}
		}
	}
	return out
}

// Claude runs the claude CLI headless for one attempt.
type Claude struct {
	// Bin is the CLI to run. Empty means look up "claude" on PATH.
	Bin     string
	Tools   Tools
	Sandbox sandbox.Policy
	// Parent is the environment to filter. Nil means this process's.
	Parent []string
}

// Run executes one attempt in dir with prompt on stdin, inside the sandbox.
// onEvent gets each stream-json event as it arrives.
func (c Claude) Run(ctx context.Context, dir, prompt string, onEvent func(Event)) (Outcome, error) {
	if len(c.Sandbox.Writable) == 0 {
		return Outcome{}, errors.New("worker: refusing to run without a sandbox policy")
	}
	bin := c.Bin
	if bin == "" {
		p, err := exec.LookPath("claude")
		if err != nil {
			return Outcome{}, ErrMissingCLI
		}
		bin = p
	}
	tools := c.Tools
	if len(tools.Allowed) == 0 && len(tools.Disallowed) == 0 {
		tools = DefaultTools
	}
	argv, err := sandbox.Wrap(c.Sandbox, append([]string{bin}, ClaudeArgs(tools)...)...)
	if err != nil {
		return Outcome{}, fmt.Errorf("worker sandbox: %w", err)
	}
	parent := c.Parent
	if parent == nil {
		parent = os.Environ()
	}
	env := append(FilterEnv(parent, WorkerEnvAllow), gitIdentity...)
	return Run(ctx, Process{
		Argv:    argv,
		Dir:     dir,
		Env:     env,
		Stdin:   prompt,
		OnEvent: onEvent,
	})
}
