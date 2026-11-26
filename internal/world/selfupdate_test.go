package world

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeGit answers the git commands the update check runs.
type fakeGit struct {
	calls  []string
	behind string
	ahead  string
	dirty  string
	remote string
	log    string
	fetch  error
}

func (g *fakeGit) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	line := name + " " + strings.Join(args, " ")
	g.calls = append(g.calls, line)
	switch {
	case strings.Contains(line, " fetch "):
		return nil, g.fetch
	case strings.Contains(line, "rev-list --count HEAD..@{u}"):
		return []byte(g.behind + "\n"), nil
	case strings.Contains(line, "rev-list --count @{u}..HEAD"):
		return []byte(g.ahead + "\n"), nil
	case strings.Contains(line, "status --porcelain"):
		return []byte(g.dirty), nil
	case strings.Contains(line, "show @{u}:internal/world/version.go"):
		return []byte(g.remote), nil
	case strings.Contains(line, "log --format=%h%x09%s HEAD..@{u}"):
		return []byte(g.log), nil
	}
	return nil, errors.New("unexpected: " + line)
}

const remoteVersionFile = "package world\n\n// Version is the aiagent release.\nconst Version = \"0.30.0\"\n"

func TestUpdateCheckSaysWhatIsNew(t *testing.T) {
	g := &fakeGit{behind: "2", ahead: "0", remote: remoteVersionFile, log: "abc1234\tfeat: one\ndef5678\tfix: two\n"}
	info := CheckUpdate(context.Background(), g, "/src/aiagent")
	if info.Error != "" || info.Current != Version || info.Latest != "0.30.0" || info.Behind != 2 || !info.CanUpdate {
		t.Fatalf("info = %+v", info)
	}
	if len(info.Commits) != 2 || info.Commits[0].Sha != "abc1234" || info.Commits[1].Subject != "fix: two" {
		t.Fatalf("commits = %+v", info.Commits)
	}
	for _, c := range g.calls {
		if !strings.HasPrefix(c, "git -C /src/aiagent ") {
			t.Errorf("every git command runs in the source checkout: %q", c)
		}
	}
}

func TestUpdateCheckRefusesWhatCannotFastForward(t *testing.T) {
	for name, g := range map[string]*fakeGit{
		"up to date":          {behind: "0", ahead: "0", remote: remoteVersionFile},
		"local commits":       {behind: "1", ahead: "1", remote: remoteVersionFile},
		"uncommitted changes": {behind: "1", ahead: "0", dirty: " M web/app.js\n", remote: remoteVersionFile},
		"the fetch failed":    {fetch: errors.New("no network")},
	} {
		info := CheckUpdate(context.Background(), g, "/src/aiagent")
		if info.CanUpdate {
			t.Errorf("%s: must not offer an update: %+v", name, info)
		}
		if name != "up to date" && info.Error == "" && info.Reason == "" {
			t.Errorf("%s: must say why", name)
		}
	}
}

func TestUpdateCheckWithoutASourceCheckout(t *testing.T) {
	info := CheckUpdate(context.Background(), &fakeGit{}, "")
	if info.CanUpdate || info.Error == "" {
		t.Fatalf("no checkout: %+v", info)
	}
}

// The source checkout is the one install.sh last installed from, unless a
// flag names one.
func TestUpdateRepoComesFromTheInstallRecord(t *testing.T) {
	e := newEnv(t)
	if got := e.svc.updateRepo(); got != "" {
		t.Fatalf("nothing recorded: %q", got)
	}
	src := t.TempDir()
	if err := os.MkdirAll(filepath.Join(src, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(e.store.dir, updateSourceFile), src+"\n")
	if got := e.svc.updateRepo(); got != src {
		t.Fatalf("recorded = %q", got)
	}
	writeFile(t, filepath.Join(e.store.dir, updateSourceFile), "/does/not/exist\n")
	if got := e.svc.updateRepo(); got != "" {
		t.Fatalf("a missing checkout is no checkout: %q", got)
	}
	e.svc.UpdateRepo = src
	if got := e.svc.updateRepo(); got != src {
		t.Fatalf("the flag wins: %q", got)
	}
}

func TestUpdateRoutesAreAdminOnly(t *testing.T) {
	e := newEnv(t)
	admin, _ := e.store.Enable("test")
	_, user, _ := e.store.AddUser("nok", "")
	if code, _, _ := e.do(t, "GET", "/aiagent/api/update", user, ""); code != 403 {
		t.Fatalf("a user reads the update check: %d", code)
	}
	if code, _, _ := e.do(t, "POST", "/aiagent/api/update", user, "{}"); code != 403 {
		t.Fatalf("a user starts an update: %d", code)
	}
	code, m, _ := e.do(t, "GET", "/aiagent/api/update", admin, "")
	if code != 200 {
		t.Fatalf("admin check: %d %v", code, m)
	}
	d := m["details"].(map[string]any)
	if d["current"] != Version || d["canUpdate"] != false {
		t.Fatalf("no checkout here: %v", d)
	}
	time.Sleep(writeSpacing)
	if code, _, _ := e.do(t, "POST", "/aiagent/api/update", admin, "{}"); code != 409 {
		t.Fatalf("nothing to update to must be refused: %d", code)
	}
}

func TestUpdateStatusIsReadBack(t *testing.T) {
	e := newEnv(t)
	writeUpdateStatus(e.store.dir, UpdateStatus{Running: false, OK: true, From: "0.29.0", To: "0.30.0", At: time.Date(2026, 11, 26, 13, 0, 0, 0, time.UTC), Log: "done"})
	st := readUpdateStatus(e.store.dir)
	if !st.OK || st.To != "0.30.0" || st.Log != "done" {
		t.Fatalf("status = %+v", st)
	}
	fi, err := os.Stat(filepath.Join(e.store.dir, updateStatusFile))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("status file %v %v", fi, err)
	}
}
