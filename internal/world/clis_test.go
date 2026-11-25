package world

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeCLI records every command and reports a version that moves to its
// `next` value once the matching CLI has been updated.
type fakeCLI struct {
	calls    []string
	versions map[string]string
	next     map[string]string
}

func (f *fakeCLI) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	line := strings.Join(append([]string{name}, args...), " ")
	f.calls = append(f.calls, line)
	if len(args) == 1 && args[0] == "--version" {
		return []byte(f.versions[filepath.Base(name)] + " (test)\n"), nil
	}
	for cli, v := range f.next {
		if strings.Contains(line, cli) {
			f.versions[cli] = v
		}
	}
	return nil, nil
}

// installedCLIs puts a native claude, an npm-installed codex and npm on a
// private PATH, and nothing else, so gemini and amp count as not installed.
func installedCLIs(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	native := filepath.Join(root, ".local", "share", "claude", "versions", "2.1.318")
	npmCodex := filepath.Join(root, "lib", "node_modules", "@openai", "codex", "bin", "codex.js")
	for _, p := range []string{native, npmCodex, filepath.Join(bin, "npm")} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(native, filepath.Join(bin, "claude")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(npmCodex, filepath.Join(bin, "codex")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	t.Setenv("HOME", filepath.Join(root, "home"))
	return bin
}

func TestNewestClaudeModelsPicksTheLatestIDPerFamily(t *testing.T) {
	bin := []byte("...claude-opus-4-8\x00claude-opus-5-5\x00claude-opus-4-1-20250805\x00" +
		"claude-fable-5-1\x00claude-fable-5-2\x00claude-sonnet-4-6claude-sonnet-5-5" +
		"claude-haiku-4-5-20251001 claude-haiku-5-5 claude-sonnet-5-5-")

	got := newestClaudeModels(bin)

	want := [][2]string{
		{"claude-fable-5-2", "Fable 5.2"},
		{"claude-opus-5-5", "Opus 5.5"},
		{"claude-sonnet-5-5", "Sonnet 5.5"},
		{"claude-haiku-5-5", "Haiku 5.5"},
		{"opus", "Opus (ล่าสุด)"}, {"sonnet", "Sonnet (ล่าสุด)"}, {"haiku", "Haiku (ล่าสุด)"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %v; want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("entry %d: got %v; want %v", i, got[i], want[i])
		}
	}
}

func TestNewestClaudeModelsIgnoresDatedAliases(t *testing.T) {
	got := newestClaudeModels([]byte("claude-opus-4-20250514\x00claude-sonnet-4-5\x00claude-sonnet-4-20250514\x00"))
	want := [][2]string{
		{"claude-sonnet-4-5", "Sonnet 4.5"},
		{"opus", "Opus (ล่าสุด)"}, {"sonnet", "Sonnet (ล่าสุด)"}, {"haiku", "Haiku (ล่าสุด)"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %v; want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("entry %d: got %v; want %v", i, got[i], want[i])
		}
	}
}

func TestNewestClaudeModelsWithoutAnyIDKeepsTheAliases(t *testing.T) {
	got := newestClaudeModels([]byte("nothing useful here"))
	if len(got) != 3 || got[0][0] != "opus" {
		t.Fatalf("got %v; want only the three aliases", got)
	}
}

func TestPlanCLIUpdatesChoosesTheMethodFromTheInstall(t *testing.T) {
	bin := installedCLIs(t)
	c := &Collector{ClaudePath: filepath.Join(bin, "claude")}

	plan := c.planCLIUpdates()

	methods := map[string]string{}
	for _, p := range plan {
		methods[p.Name] = p.Method
	}
	if methods["claude"] != "native" || methods["codex"] != "npm" || len(methods) != 2 {
		t.Fatalf("plan methods = %v; want claude native, codex npm, nothing else", methods)
	}
}

func TestUpdateCLIsRunsEachUpdaterAndReportsTheVersionChange(t *testing.T) {
	bin := installedCLIs(t)
	f := &fakeCLI{
		versions: map[string]string{"claude": "2.1.318", "codex": "0.166.0"},
		next:     map[string]string{"claude": "2.1.319", "codex": "0.167.0"},
	}
	c := &Collector{Runner: f, ClaudePath: filepath.Join(bin, "claude")}

	c.runCLIUpdates(context.Background())

	results := c.CLIUpdateStatus().Results
	if len(results) != 2 {
		t.Fatalf("results = %+v; want claude and codex", results)
	}
	for _, r := range results {
		if r.Error != "" {
			t.Errorf("%s failed: %s", r.Name, r.Error)
		}
		switch r.Name {
		case "claude":
			if r.Before != "2.1.318" || r.After != "2.1.319" || r.Method != "native" {
				t.Errorf("claude result = %+v", r)
			}
		case "codex":
			if r.Before != "0.166.0" || r.After != "0.167.0" || r.Method != "npm" {
				t.Errorf("codex result = %+v", r)
			}
		}
	}
	wantCalls := []string{
		filepath.Join(bin, "claude") + " --version",
		filepath.Join(bin, "claude") + " update",
		filepath.Join(bin, "claude") + " --version",
		filepath.Join(bin, "codex") + " --version",
		"npm install -g --prefix " + filepath.Dir(bin) + " @openai/codex@latest", // the prefix codex came from
		filepath.Join(bin, "codex") + " --version",
	}
	if strings.Join(f.calls, "\n") != strings.Join(wantCalls, "\n") {
		t.Errorf("calls:\n%s\nwant:\n%s", strings.Join(f.calls, "\n"), strings.Join(wantCalls, "\n"))
	}
}

func TestStartCLIUpdateRefusesASecondRunWhileOneIsActive(t *testing.T) {
	bin := installedCLIs(t)
	release := make(chan struct{})
	c := &Collector{Runner: blockingRunner{release: release}, ClaudePath: filepath.Join(bin, "claude")}

	done, err := c.StartCLIUpdate()
	if err != nil {
		t.Fatalf("first start: %v", err)
	}
	if _, err := c.StartCLIUpdate(); err == nil {
		t.Fatal("second start while running succeeded; want a conflict")
	}
	close(release)
	<-done
	if c.CLIUpdateStatus().Running {
		t.Fatal("still marked running after the run finished")
	}
}

type blockingRunner struct{ release chan struct{} }

func (b blockingRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	if len(args) == 1 && args[0] == "--version" {
		return []byte("1.0.0\n"), nil
	}
	<-b.release
	return nil, nil
}

// A CLI installed after the service started is found without a restart.
func TestCLIFoundAfterStart(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", t.TempDir())
	c := &Collector{}
	if got := c.ClaudeBin(); got != "claude" {
		t.Fatalf("before install = %q", got)
	}
	p := filepath.Join(home, ".local", "bin", "claude")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := c.ClaudeBin(); got != p {
		t.Fatalf("after install = %q, want %q", got, p)
	}
}
