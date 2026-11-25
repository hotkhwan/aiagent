package world

import (
	"context"
	"path/filepath"
	"testing"
)

// IsAgentCommand decides whether text may be typed into a pane, from the
// command tmux reports for it. It is an allowlist: anything not known to be an
// agent CLI is refused, because text typed into an editor, a REPL or a pager
// does something the operator did not ask for.
func TestIsAgentCommandIsAnAllowlistOfAgentCLIs(t *testing.T) {
	cases := []struct {
		cmd  string
		want bool
	}{
		{"claude", true},
		// On macOS ~/.local/bin/claude links to a binary named after its
		// version, and tmux reports that name.
		{"2.1.318", true},
		{"codex", true},
		{"gemini", true},
		{"agy", true},
		// node alone is not enough: a node REPL is node too. A node pane is
		// accepted only once its argv shows codex or gemini (PaneAgent).
		{"node", false},

		{"zsh", false},
		{"bash", false},
		{"", false},
		{"tmux", false},
		{"vim", false},
		{"python3", false},
		{"less", false},
		{"2.1", false},
		{"1.2.3.4", false},
		{"v2.1.318", false},
	}
	for _, c := range cases {
		if got := IsAgentCommand(c.cmd); got != c.want {
			t.Errorf("IsAgentCommand(%q) = %v, want %v", c.cmd, got, c.want)
		}
	}
}

// codex and gemini installed through npm show in tmux as node, and so does a
// bare node REPL, which would run typed text as JavaScript. A node pane is an
// agent only when the process tree under it shows which CLI it is.
func TestNodePaneIsAnAgentOnlyWhenItRunsCodexOrGemini(t *testing.T) {
	pt := procTable{
		100: {ppid: 1, argv: []string{"node", "/opt/homebrew/bin/codex"}},
		200: {ppid: 1, argv: []string{"node"}},
		300: {ppid: 1, argv: []string{"-zsh"}},
		301: {ppid: 300, argv: []string{"node", "/opt/homebrew/lib/node_modules/@google/gemini-cli/dist/index.js"}},
		400: {ppid: 1, argv: []string{"node", "server.js"}},
	}
	cases := []struct {
		cmd  string
		pid  int
		want string
	}{
		{"node", 100, "codex"},
		{"node", 200, ""},
		{"node", 300, "gemini"},
		{"agy", 1, "gemini"},
		{"node", 400, ""},
		{"2.1.318", 0, "claude"},
		{"claude", 0, "claude"},
		{"codex", 0, "codex"},
		{"vim", 0, ""},
	}
	for _, c := range cases {
		if got := paneAgent(pt, c.cmd, c.pid); got != c.want {
			t.Errorf("paneAgent(%q, %d) = %q, want %q", c.cmd, c.pid, got, c.want)
		}
	}
}

// The workspace table the PM routes by must agree: a codex pane is ready, a
// node REPL left in an agent window is not.
func TestWorkspaceWithANodeReplIsNotReady(t *testing.T) {
	e := newEnv(t)
	fakeProcPPID(t, e.proc, "5100", "1", "1")
	writeFile(t, filepath.Join(e.proc, "5100", "cmdline"), "node\x00/opt/homebrew/bin/codex\x00")
	fakeProcPPID(t, e.proc, "5200", "1", "1")
	writeFile(t, filepath.Join(e.proc, "5200", "cmdline"), "node\x00")
	e.runner.sessions = "api\t1\nweb\t1\n"
	e.runner.panes = "api\tagent\t1\tnode\t5100\nweb\tagent\t1\tnode\t5200\n"

	ready := map[string]bool{}
	for _, w := range e.col.Snapshot(context.Background()).Workspaces {
		ready[w.Name] = w.AgentReady
	}
	if !ready["api"] {
		t.Error("a node pane running codex should be ready")
	}
	if ready["web"] {
		t.Error("a node REPL must not be treated as an agent")
	}
}
