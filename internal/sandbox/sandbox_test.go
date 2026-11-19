package sandbox

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
)

func requireDarwin(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "darwin" {
		t.Skip("sandbox-exec is macOS only")
	}
	if _, err := exec.LookPath("sandbox-exec"); err != nil {
		t.Skip("sandbox-exec not installed")
	}
}

// A worker may write inside its worktree and temp dir and nowhere else, and it
// may not read the operator's secrets. HOME is a temp dir so the test never
// touches the real ~/.ssh.
func TestWorkerCannotWriteOutsideWorktreeOrReadSecrets(t *testing.T) {
	requireDarwin(t)
	root := t.TempDir()
	home := filepath.Join(root, "home")
	work := filepath.Join(root, "wt")
	state := filepath.Join(root, "state")
	outside := filepath.Join(root, "outside")
	for _, d := range []string{filepath.Join(home, ".ssh"), work, state, outside} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(home, ".ssh", "id_test"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(state, "tasks.jsonl"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}

	p := Policy{
		Writable: []string{work},
		Denied:   []string{filepath.Join(home, ".ssh"), state},
	}
	script := `echo ok > "$WORK/made" || exit 10
echo x > "$OUTSIDE/no" 2>/dev/null && exit 11
cat "$SSH/id_test" >/dev/null 2>&1 && exit 12
cat "$STATE/tasks.jsonl" >/dev/null 2>&1 && exit 13
exit 0`
	argv, err := Wrap(p, "/bin/sh", "-c", script)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(context.Background(), argv[0], argv[1:]...)
	cmd.Env = []string{"WORK=" + work, "OUTSIDE=" + outside, "SSH=" + filepath.Join(home, ".ssh"), "STATE=" + state, "PATH=/usr/bin:/bin"}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("sandboxed script failed: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(work, "made")); err != nil {
		t.Fatalf("write inside the worktree should succeed: %v", err)
	}
}

// The production policy writes only into the attempt's worktree and temp dir,
// and denies the operator's state and credentials.
func TestDefaultPolicyWritesOnlyTheAttemptAndDeniesSecrets(t *testing.T) {
	p := DefaultPolicy("/Users/op", "/Users/op/.aiagent", "/Users/op/.aiagent/worktrees/A1", "/var/tmp/A1")
	if !slices.Equal(p.Writable, []string{"/Users/op/.aiagent/worktrees/A1", "/var/tmp/A1"}) {
		t.Fatalf("writable = %v", p.Writable)
	}
	for _, want := range []string{"/Users/op/.ssh", "/Users/op/.cloudflared", "/Users/op/.aiagent", "/Users/op/Library/Keychains"} {
		if !slices.Contains(p.Denied, want) {
			t.Fatalf("%s is not denied: %v", want, p.Denied)
		}
	}
}
