package worker

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/hotkhwan/aiagent/internal/sandbox"
)

func TestClaudeArgsKeepThePromptOutOfArgvAndPinTheTools(t *testing.T) {
	args := ClaudeArgs(DefaultTools)
	joined := strings.Join(args, " ")
	for _, want := range []string{"-p", "--output-format stream-json", "--verbose", "--allowedTools", "--disallowedTools"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("argv %q lacks %q", joined, want)
		}
	}
	if !slices.Contains(DefaultTools.Disallowed, "Bash(git push:*)") {
		t.Fatal("git push must be disallowed")
	}
	if slices.Contains(DefaultTools.Allowed, "Bash(git push:*)") {
		t.Fatal("git push must not be allowed")
	}
}

func TestFilterEnvPassesOnlyTheAllowlist(t *testing.T) {
	parent := []string{
		"PATH=/usr/bin:/bin",
		"HOME=/Users/op",
		"GITHUB_TOKEN=ghp_secret",
		"ANTHROPIC_API_KEY=sk-ant-secret",
		"AWS_SECRET_ACCESS_KEY=aws-secret",
		"LANG=th_TH.UTF-8",
	}
	got := FilterEnv(parent, WorkerEnvAllow)
	joined := strings.Join(got, "\n")
	if !strings.Contains(joined, "PATH=/usr/bin:/bin") || !strings.Contains(joined, "LANG=th_TH.UTF-8") {
		t.Fatalf("allowed names missing: %q", joined)
	}
	if strings.Contains(joined, "secret") {
		t.Fatalf("a secret leaked into the worker env: %q", joined)
	}
}

// The worker logs in with the subscription token from `claude setup-token`,
// not with the keychain, so the token is the one credential that crosses over.
func TestWorkerEnvCarriesOnlyTheSubscriptionToken(t *testing.T) {
	parent := []string{
		"CLAUDE_CODE_OAUTH_TOKEN=oauth-token-value",
		"ANTHROPIC_API_KEY=sk-ant-api-value",
		"ANTHROPIC_AUTH_TOKEN=bearer-value",
		"GITHUB_TOKEN=ghp-value",
	}
	got := FilterEnv(parent, WorkerEnvAllow)
	want := []string{"CLAUDE_CODE_OAUTH_TOKEN=oauth-token-value"}
	if !slices.Equal(got, want) {
		t.Fatalf("worker env = %q, want %q", got, want)
	}
}

// An absent CLI is its own error, so the orchestrator can block the task with
// missingCli instead of treating it as a crash.
func TestMissingCLIIsItsOwnError(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	c := Claude{Sandbox: sandbox.Policy{Writable: []string{t.TempDir()}}}
	_, err := c.Run(context.Background(), t.TempDir(), "prompt", nil)
	if !errors.Is(err, ErrMissingCLI) {
		t.Fatalf("err = %v, want ErrMissingCLI", err)
	}
}

func TestClaudeRefusesToRunWithoutASandbox(t *testing.T) {
	_, err := Claude{Bin: "/bin/echo"}.Run(context.Background(), t.TempDir(), "p", nil)
	if err == nil {
		t.Fatal("a worker with no sandbox policy must not start")
	}
}

// The fake CLI records its argv, stdin and environment, then prints a result
// line. It runs under the real sandbox, so the directory it writes to is in the
// policy.
func TestClaudeRunsTheCLIWithPromptOnStdinAndFilteredEnv(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("sandbox-exec is macOS only")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "claude")
	script := strings.ReplaceAll(`#!/bin/sh
printf "%s\n" "$@" > OUT/argv
cat > OUT/stdin
env > OUT/env
echo '{"type":"result","subtype":"success","is_error":false,"result":"done"}'
`, "OUT", dir)
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GITHUB_TOKEN", "ghp_secret")
	work := t.TempDir()

	var events []Event
	out, err := Claude{
		Bin:     bin,
		Sandbox: sandbox.Policy{Writable: []string{dir, work}},
	}.Run(context.Background(), work, "ทำ task นี้", func(e Event) { events = append(events, e) })
	if err != nil {
		t.Fatal(err)
	}
	if out.ExitErr != nil {
		t.Fatalf("exit: %v\n%s", out.ExitErr, out.Stderr)
	}
	if len(events) != 1 || events[0].Kind != "result" || events[0].Text != "done" {
		t.Fatalf("events = %+v", events)
	}
	argv, _ := os.ReadFile(filepath.Join(dir, "argv"))
	if !strings.Contains(string(argv), "stream-json") || strings.Contains(string(argv), "ทำ") {
		t.Fatalf("argv = %s", argv)
	}
	stdin, _ := os.ReadFile(filepath.Join(dir, "stdin"))
	if string(stdin) != "ทำ task นี้" {
		t.Fatalf("stdin = %q", stdin)
	}
	env, _ := os.ReadFile(filepath.Join(dir, "env"))
	if strings.Contains(string(env), "GITHUB_TOKEN") {
		t.Fatal("the worker environment carried GITHUB_TOKEN")
	}
}

func TestWorkerToolsDoNotAllowGoCommands(t *testing.T) {
	for _, tool := range DefaultTools.Allowed {
		if strings.HasPrefix(tool, "Bash(go") || strings.HasPrefix(tool, "Bash(gofmt") {
			t.Fatalf("worker may run %s; the token process must not execute test code", tool)
		}
	}
}

func TestWorkerGitDoesNotRunAutomaticMaintenance(t *testing.T) {
	cmd := exec.Command("git", "config", "--get", "maintenance.auto")
	cmd.Env = workerEnv(os.Environ(), "")
	out, _ := cmd.Output()
	if strings.TrimSpace(string(out)) != "false" {
		t.Fatalf("maintenance.auto = %q under the worker environment; want false", out)
	}
}
