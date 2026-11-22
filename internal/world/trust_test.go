package world

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// claudeJSONFixture stands in for ~/.claude.json: other state Claude Code
// keeps, in its own order, that an edit must leave exactly as it was.
const claudeJSONFixture = `{
  "numStartups": 412,
  "zeta": {
    "nested": [
      1,
      2.50,
      "<b>&</b>"
    ]
  },
  "projects": {
    "/elsewhere/kept": {
      "allowedTools": [],
      "hasTrustDialogAccepted": false,
      "lastCost": 1.25e-3
    }
  },
  "alpha": "last"
}
`

func writeClaudeJSON(t *testing.T, home string, mode os.FileMode) string {
	t.Helper()
	p := filepath.Join(home, ".claude.json")
	if err := os.WriteFile(p, []byte(claudeJSONFixture), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, mode); err != nil {
		t.Fatal(err)
	}
	return p
}

// realTemp is a temp dir with its own symlinks resolved (macOS's /var is
// /private/var), so the expected paths below are exact.
func realTemp(t *testing.T) string {
	t.Helper()
	d, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func mkdirs(t *testing.T, ps ...string) {
	t.Helper()
	for _, p := range ps {
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

func TestEnsureFolderTrusted(t *testing.T) {
	base := realTemp(t)
	root := filepath.Join(base, "projects")
	mkdirs(t, filepath.Join(root, "app", "deep", "er"), filepath.Join(base, "projectsX", "app"), filepath.Join(base, "other"))
	if err := os.Symlink(filepath.Join(root, "app"), filepath.Join(base, "link")); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name  string
		roots []string
		dir   string
		want  []string // paths newly trusted; nil with wantErr
		err   error
	}{
		{"inside a root", []string{root}, filepath.Join(root, "app"), []string{filepath.Join(root, "app")}, nil},
		{"the root itself", []string{root}, root, []string{root}, nil},
		{"nested", []string{root}, filepath.Join(root, "app", "deep", "er"), []string{filepath.Join(root, "app", "deep", "er")}, nil},
		{"outside", []string{root}, filepath.Join(base, "other"), nil, ErrNotUnderRoot},
		{"sibling with the root as a prefix", []string{root}, filepath.Join(base, "projectsX", "app"), nil, ErrNotUnderRoot},
		{"dot-dot out of the root", []string{root}, root + "/app/../../projectsX/app", nil, ErrNotUnderRoot},
		{"symlinked folder whose real path is under the root", []string{root}, filepath.Join(base, "link"), []string{filepath.Join(base, "link"), filepath.Join(root, "app")}, nil},
		{"root given with a trailing slash", []string{root + "/"}, filepath.Join(root, "app"), []string{filepath.Join(root, "app")}, nil},
		{"one of several roots", []string{filepath.Join(base, "nope"), root}, filepath.Join(root, "app"), []string{filepath.Join(root, "app")}, nil},
		{"empty config trusts nothing", nil, filepath.Join(root, "app"), nil, ErrNotUnderRoot},
		{"relative folder", []string{root}, "projects/app", nil, ErrNotUnderRoot},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			p := writeClaudeJSON(t, home, 0o600)
			got, err := EnsureFolderTrusted(home, tc.dir, tc.roots)
			if !errors.Is(err, tc.err) {
				t.Fatalf("err = %v, want %v", err, tc.err)
			}
			if !slices.Equal(got, tc.want) {
				t.Fatalf("trusted %q, want %q", got, tc.want)
			}
			b, _ := os.ReadFile(p)
			if tc.err != nil {
				if string(b) != claudeJSONFixture {
					t.Fatal(".claude.json changed although the folder is not under a root")
				}
				return
			}
			for _, w := range tc.want {
				if ok, err := FolderTrusted(home, w); err != nil || !ok {
					t.Fatalf("%s not trusted in .claude.json (err %v)", w, err)
				}
			}
			// a second call finds it trusted and writes nothing
			again, err := EnsureFolderTrusted(home, tc.dir, tc.roots)
			if err != nil || len(again) != 0 {
				t.Fatalf("second call: changed %q, err %v; want nothing", again, err)
			}
		})
	}
}

// Everything else in the file survives byte for byte, in its order, and the
// file keeps its mode.
func TestEnsureFolderTrustedPreservesTheRest(t *testing.T) {
	for _, mode := range []os.FileMode{0o600, 0o644} {
		t.Run(mode.String(), func(t *testing.T) {
			base := realTemp(t)
			dir := filepath.Join(base, "root", "proj")
			mkdirs(t, dir)
			home := t.TempDir()
			p := writeClaudeJSON(t, home, mode)
			if _, err := EnsureFolderTrusted(home, dir, []string{filepath.Join(base, "root")}); err != nil {
				t.Fatal(err)
			}
			fi, err := os.Stat(p)
			if err != nil {
				t.Fatal(err)
			}
			if fi.Mode().Perm() != mode {
				t.Fatalf("mode = %v, want %v", fi.Mode().Perm(), mode)
			}
			b, _ := os.ReadFile(p)
			want := strings.Replace(claudeJSONFixture, `      "lastCost": 1.25e-3
    }
  },`, `      "lastCost": 1.25e-3
    },
    "`+dir+`": {
      "hasTrustDialogAccepted": true
    }
  },`, 1)
			if string(b) != want {
				t.Fatalf("file after the edit:\n%s\nwant:\n%s", b, want)
			}
			// nothing but the entry was added: removing it restores the original
			if ok, err := ForgetFolderTrust(home, dir); err != nil || !ok {
				t.Fatalf("forget: %v %v", ok, err)
			}
			if b, _ := os.ReadFile(p); string(b) != claudeJSONFixture {
				t.Fatalf("after forget:\n%s", b)
			}
			if left, _ := filepath.Glob(filepath.Join(home, ".claude.json.aiagent-*")); len(left) > 0 {
				t.Fatalf("temporary files left behind: %q", left)
			}
		})
	}
}

// An entry Claude Code already has keeps its other keys and their order.
func TestEnsureFolderTrustedKeepsAnExistingEntry(t *testing.T) {
	home := t.TempDir()
	writeClaudeJSON(t, home, 0o600)
	if _, err := EnsureFolderTrusted(home, "/elsewhere/kept", []string{"/elsewhere"}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(home, ".claude.json"))
	want := strings.Replace(claudeJSONFixture, `"hasTrustDialogAccepted": false`, `"hasTrustDialogAccepted": true`, 1)
	if string(b) != want {
		t.Fatalf("got:\n%s\nwant:\n%s", b, want)
	}
}

// No ~/.claude.json yet: one is created with just the entry.
func TestEnsureFolderTrustedCreatesTheFile(t *testing.T) {
	home := t.TempDir()
	if _, err := EnsureFolderTrusted(home, "/srv/x/y", []string{"/srv/x"}); err != nil {
		t.Fatal(err)
	}
	var top map[string]map[string]map[string]bool
	b, _ := os.ReadFile(filepath.Join(home, ".claude.json"))
	if err := json.Unmarshal(b, &top); err != nil || !top["projects"]["/srv/x/y"]["hasTrustDialogAccepted"] {
		t.Fatalf("file = %s (%v)", b, err)
	}
}

// Claude Code saving the file while we edit it: its change is kept and ours is
// re-applied on top; a file that never stops changing is an error, unwritten.
func TestEnsureFolderTrustedRetriesAConcurrentWrite(t *testing.T) {
	home := t.TempDir()
	p := writeClaudeJSON(t, home, 0o600)
	writes := 0
	beforeClaudeJSONCheck = func() {
		if writes == 0 {
			writeFile(t, p, `{"numStartups": 413, "projects": {}}`)
		}
		writes++
	}
	t.Cleanup(func() { beforeClaudeJSONCheck = nil })
	if _, err := EnsureFolderTrusted(home, "/srv/x/y", []string{"/srv/x"}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	if !strings.Contains(string(b), `"numStartups": 413`) {
		t.Fatalf("the concurrent write was lost: %s", b)
	}
	if ok, _ := FolderTrusted(home, "/srv/x/y"); !ok || writes != 2 {
		t.Fatalf("trusted %v after %d tries, want trusted after 2", ok, writes)
	}

	n := 0
	beforeClaudeJSONCheck = func() { n++; writeFile(t, p, `{"numStartups": `+strings.Repeat("9", n)+`}`) }
	if _, err := EnsureFolderTrusted(home, "/srv/x/z", []string{"/srv/x"}); err == nil {
		t.Fatal("want an error when the file keeps changing")
	}
	if ok, _ := FolderTrusted(home, "/srv/x/z"); ok {
		t.Fatal("written although the file kept changing")
	}
}

// A file that is not a JSON object is left alone.
func TestEnsureFolderTrustedRefusesABrokenFile(t *testing.T) {
	home := t.TempDir()
	p := filepath.Join(home, ".claude.json")
	writeFile(t, p, "[1,2]")
	if _, err := EnsureFolderTrusted(home, "/srv/x/y", []string{"/srv/x"}); err == nil {
		t.Fatal("want an error for a non-object file")
	}
	if b, _ := os.ReadFile(p); string(b) != "[1,2]" {
		t.Fatalf("file changed: %s", b)
	}
}

func TestTrustedRootsConfig(t *testing.T) {
	state := t.TempDir()
	writeFile(t, filepath.Join(state, "policy.json"), `{"enabled": true, "custom": {"x": 1}, "thresholdPct": 80}`)
	if got := LoadTrustedRoots(state); len(got) != 0 {
		t.Fatalf("default roots = %q, want none", got)
	}
	if err := SetTrustedRoots(state, []string{"/a/b/", "/a/b", "/c"}); err != nil {
		t.Fatal(err)
	}
	if got := LoadTrustedRoots(state); !slices.Equal(got, []string{"/a/b", "/c"}) {
		t.Fatalf("roots = %q", got)
	}
	b, _ := os.ReadFile(filepath.Join(state, "policy.json"))
	if !strings.Contains(string(b), `"custom": {`) || !strings.Contains(string(b), `"thresholdPct": 80`) {
		t.Fatalf("other policy keys lost: %s", b)
	}
	// a policy saved from the web keeps the roots
	svc := &Service{Store: NewStore(state)}
	if _, err := svc.SetPolicy(true, 85, "haiku", "gpt-6-luna"); err != nil {
		t.Fatal(err)
	}
	if got := LoadTrustedRoots(state); !slices.Equal(got, []string{"/a/b", "/c"}) {
		t.Fatalf("roots after a policy save = %q", got)
	}
	t.Setenv(TrustedRootsEnv, "/env/one"+string(os.PathListSeparator)+"/env/two/")
	if got := LoadTrustedRoots(state); !slices.Equal(got, []string{"/env/one", "/env/two"}) {
		t.Fatalf("env roots = %q", got)
	}
	if _, err := NormalizeRoot("/"); err == nil {
		t.Fatal("the filesystem root must be refused")
	}
}

// A review sent to a finished session in a folder under a trusted root is
// resumed and delivered without stopping at the trust question.
func TestSendUnderATrustedRootSkipsTheTrustDialog(t *testing.T) {
	e := doneJobEnv(t)
	e.runner.trustPrompt = true
	e.runner.trustHome, e.runner.trustDir = e.home, "/root"
	e.col.TrustedRoots = func() []string { return []string{"/root"} }
	if _, err := e.col.SendToSession(context.Background(), doneJob, "ตรวจแล้ว ผ่าน"); err != nil {
		t.Fatalf("send: %v", err)
	}
	if ok, _ := FolderTrusted(e.home, "/root"); !ok {
		t.Fatal("/root was not marked trusted")
	}
	if timesTyped(e, "ตรวจแล้ว ผ่าน") != 1 {
		t.Fatal("the answer was not typed")
	}
	if b, _ := os.ReadFile(e.auditFP); !strings.Contains(string(b), "aiagent.autoTrust") {
		t.Fatal("no aiagent.autoTrust audit line")
	}
}

// A relay already sitting on the trust question is restarted after trusting.
func TestARelayStuckOnTrustIsRestartedAfterTrusting(t *testing.T) {
	e := doneJobEnv(t)
	e.runner.relayUp = true
	e.runner.trustPrompt = true
	e.runner.trustHome, e.runner.trustDir = e.home, "/root"
	e.runner.paneStart = "new-session -d -s aiagent-bbbb2222 -x 220 -y 50 -c /root claude --resume " + doneJob
	e.col.TrustedRoots = func() []string { return []string{"/root"} }
	if _, err := e.col.SendToSession(context.Background(), doneJob, "ตรวจแล้ว ผ่าน"); err != nil {
		t.Fatalf("send: %v", err)
	}
	killed := false
	for _, c := range e.runner.callsTo("tmux") {
		if c[0] == "kill-session" {
			killed = true
		}
	}
	if !killed || len(relayStarts(e)) != 1 || timesTyped(e, "ตรวจแล้ว ผ่าน") != 1 {
		t.Fatalf("killed %v, starts %q: want the stuck relay replaced and the answer typed", killed, relayStarts(e))
	}
}

// Outside every root nothing is written and the error says what to do.
func TestTrustOutsideTheRootsKeepsTheError(t *testing.T) {
	e := doneJobEnv(t)
	e.runner.trustPrompt = true
	e.col.TrustedRoots = func() []string { return []string{"/srv"} }
	_, err := e.col.SendToSession(context.Background(), doneJob, "ตรวจแล้ว ผ่าน")
	if err == nil || !strings.Contains(err.Error(), "/root") || !strings.Contains(err.Error(), "trust-roots add") {
		t.Fatalf("err = %v, want the folder and how to add a root", err)
	}
	if _, err := os.Stat(filepath.Join(e.home, ".claude.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal(".claude.json was written for a folder outside the roots")
	}
}
