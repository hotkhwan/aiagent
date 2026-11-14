package world

import "testing"

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
		{"node", true}, // codex and gemini installed through npm run as node

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
