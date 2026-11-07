package world

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"

	"github.com/hotkhwan/aiagent/internal/audit"
)

// ---------- fixtures ----------

type fakeRunner struct {
	mu         sync.Mutex
	sessions   string
	panes      string
	agcmdOut   string
	calls      [][]string
	relayUp    bool
	agentsJSON string
}

func (f *fakeRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, append([]string{name}, args...))
	if name == "tmux" {
		switch args[0] {
		case "list-sessions":
			return []byte(f.sessions), nil
		case "has-session":
			if f.relayUp {
				return nil, nil
			}
			return nil, errors.New("no session")
		case "new-session":
			f.relayUp = true
			return nil, nil
		case "list-panes":
			if len(args) > 1 && args[1] == "-t" {
				return []byte("claude\n"), nil
			}
			return []byte(f.panes), nil
		case "send-keys", "capture-pane":
			// Real tmux refuses a bare "=name" here — that targets a PANE, and
			// no pane is named after the session, so it answers
			// `can't find pane`. Only "=name:" (session:window) resolves. The
			// fake used to return the prompt whatever the target, which is why
			// a relay that could never see the prompt passed its tests.
			for i, a := range args {
				if a != "-t" || i+1 >= len(args) {
					continue
				}
				t := args[i+1]
				if strings.HasPrefix(t, "=") && !strings.HasSuffix(t, ":") {
					return []byte("can't find pane: " + t), errors.New("exit status 1")
				}
			}
			if args[0] == "send-keys" {
				return nil, nil
			}
			return []byte("──\n❯ \n──"), nil
		}
		return nil, nil
	}
	if name == "claude" && len(args) > 0 && args[0] == "agents" {
		return []byte(f.agentsJSON), nil
	}
	return []byte(f.agcmdOut), nil
}

func (f *fakeRunner) callsTo(name string) [][]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out [][]string
	for _, c := range f.calls {
		if c[0] == name {
			out = append(out, c[1:])
		}
	}
	return out
}

func (f *fakeRunner) agcmdCalls() [][]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out [][]string
	for _, c := range f.calls {
		if c[0] == "agcmd" {
			out = append(out, c[1:])
		}
	}
	return out
}

func writeFile(t *testing.T, p, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// fakeProc writes /proc/<pid>/stat with the given starttime (field 22).
func fakeProc(t *testing.T, root, pid, start string) {
	t.Helper()
	fields := make([]string, 20)
	for i := range fields {
		fields[i] = "0"
	}
	fields[0] = "S"
	fields[19] = start
	writeFile(t, filepath.Join(root, pid, "stat"), pid+" (claude) "+strings.Join(fields, " ")+"\n")
}

type env struct {
	dir     string
	home    string
	proc    string
	runs    string
	runner  *fakeRunner
	col     *Collector
	store   *Store
	svc     *Service
	app     *fiber.App
	auditFP string
	now     time.Time
}

// testWorkspaces is the fixture workspace set. Workspaces are operator
// configuration now, so the tests declare their own instead of leaning on a
// table compiled into the binary.
func testWorkspaces() []workspaceEntry {
	return []workspaceEntry{
		{name: "api", session: "api", dir: "/home/dev/api"},
		{name: "web", session: "web", dir: "/home/dev/web"},
		{name: "docs", session: "docs", dir: "/home/dev/docs"},
	}
}

func newEnv(t *testing.T) *env {
	t.Helper()
	dir := t.TempDir()
	e := &env{dir: dir, home: filepath.Join(dir, "home", "dev"), proc: filepath.Join(dir, "proc"), runs: filepath.Join(dir, "runs"), runner: &fakeRunner{}}
	e.now = time.Date(2026, 11, 4, 10, 20, 0, 0, time.UTC)
	for _, p := range []string{e.home, e.proc, e.runs} {
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	e.col = &Collector{Homes: []string{e.home}, ProcRoot: e.proc, RunsDir: e.runs, AgcmdPath: "agcmd", TmuxPath: "tmux", Runner: e.runner, Now: func() time.Time { return e.now }, CacheTTL: 0, HistoryMax: 50, ClaudePath: "claude", ServiceHome: e.home, Sleep: func(time.Duration) {}, Workspaces: testWorkspaces()}
	e.store = NewStore(filepath.Join(dir, "state"))
	e.auditFP = filepath.Join(dir, "audit.log")
	auditor, err := audit.Open(e.auditFP)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { auditor.Close() })
	e.svc = NewService(e.store, e.col, auditor)
	e.app = fiber.New()
	e.svc.Mount(e.app)
	e.app.Get("/*", func(c fiber.Ctx) error { return c.SendString("WEBINIT-SPA") })
	return e
}

func (e *env) do(t *testing.T, method, path, token, body string) (int, map[string]any, string) {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, r)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set(TokenHeader, token)
	}
	resp, err := e.app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	return resp.StatusCode, m, string(b)
}

// ---------- switch / auth ----------

func TestStoreSwitch(t *testing.T) {
	s := NewStore(t.TempDir())
	if s.Enabled() || s.VerifyToken("x") {
		t.Fatal("fresh store must be disabled")
	}
	tok, err := s.Enable("test")
	if err != nil {
		t.Fatal(err)
	}
	if !s.Enabled() || !s.VerifyToken(tok) || s.VerifyToken(tok+"x") || s.VerifyToken("") {
		t.Fatal("token verification wrong")
	}
	fi, err := os.Stat(filepath.Join(s.dir, stateFileName))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("state file must be 0600, got %v %v", fi.Mode().Perm(), err)
	}
	b, _ := os.ReadFile(filepath.Join(s.dir, stateFileName))
	if strings.Contains(string(b), tok) {
		t.Fatal("plaintext token must never be stored")
	}
	tok2, _ := s.Enable("rotate")
	if s.VerifyToken(tok) || !s.VerifyToken(tok2) {
		t.Fatal("re-enable must rotate the token")
	}
	if err := s.Disable("test"); err != nil {
		t.Fatal(err)
	}
	if s.Enabled() || s.VerifyToken(tok2) {
		t.Fatal("disable must void the token")
	}
	writeFile(t, filepath.Join(s.dir, stateFileName), "{not json")
	if s.Enabled() {
		t.Fatal("corrupt switch file must fail closed")
	}
}

func TestDisabledIsInvisible(t *testing.T) {
	e := newEnv(t)
	for _, p := range []string{"/aiagent", "/aiagent/", "/aiagent/app.js", "/aiagent/api/snapshot", "/aiagent/whatever", "/aiagent/assets/house-kcmd.png"} {
		code, _, body := e.do(t, "GET", p, "", "")
		if code != 404 || strings.Contains(body, "WEBINIT-SPA") {
			t.Fatalf("%s while disabled: %d %q", p, code, body)
		}
	}
}

func TestTokenGate(t *testing.T) {
	e := newEnv(t)
	tok, _ := e.store.Enable("test")
	req := httptest.NewRequest("GET", "/aiagent", nil)
	resp, _ := e.app.Test(req)
	if resp.StatusCode != 200 || !strings.Contains(resp.Header.Get("Content-Security-Policy"), "script-src 'self'") {
		t.Fatalf("page must be served with CSP when enabled: %d", resp.StatusCode)
	}
	if code, _, _ := e.do(t, "GET", "/aiagent/api/snapshot", "", ""); code != 401 {
		t.Fatalf("no token: %d", code)
	}
	if code, m, _ := e.do(t, "GET", "/aiagent/api/snapshot", tok, ""); code != 200 || m["code"] != "SUCCESS" {
		t.Fatalf("good token: %d %v", code, m)
	}
	if code, _, _ := e.do(t, "GET", "/aiagent/assets/house-kcmd.png", "", ""); code != 200 {
		t.Fatalf("asset: %d", code)
	}
	for _, bad := range []string{"/aiagent/assets/..%2Fapp.js", "/aiagent/assets/app.js", "/aiagent/assets/nope.png"} {
		if code, _, _ := e.do(t, "GET", bad, "", ""); code != 404 {
			t.Fatalf("%s must 404: %d", bad, code)
		}
	}
	if code, _, body := e.do(t, "GET", "/aiagent/nope", tok, ""); code != 404 || strings.Contains(body, "WEBINIT-SPA") {
		t.Fatalf("unknown /aiagent path must 404, not fall to the SPA: %d", code)
	}
	for i := 0; i < maxFailures; i++ {
		e.do(t, "GET", "/aiagent/api/snapshot", "wrong", "")
	}
	if code, _, _ := e.do(t, "GET", "/aiagent/api/snapshot", tok, ""); code != 429 {
		t.Fatalf("after %d wrong tokens the client must be locked out: %d", maxFailures, code)
	}
}

// ---------- write path ----------

func TestMissionRefusesShellAndSendsExactArgv(t *testing.T) {
	e := newEnv(t)
	tok, _ := e.store.Enable("test")
	e.runner.sessions = "web\t1793750000\n"
	e.runner.panes = "web\tagent\t1\tbash\n"

	code, m, _ := e.do(t, "POST", "/aiagent/api/missions", tok, `{"workspace":"web","task":"แก้ test หน้า login"}`)
	if code != 409 || len(e.runner.agcmdCalls()) != 0 {
		t.Fatalf("a shell in the agent window must be refused before agcmd runs: %d %v %v", code, m, e.runner.agcmdCalls())
	}

	e.runner.panes = "web\tagent\t1\tclaude\n"
	e.runner.agcmdOut = "ENG-20261104-001  RUNNING  web  แก้ test"
	time.Sleep(writeSpacing)
	code, m, _ = e.do(t, "POST", "/aiagent/api/missions", tok, `{"workspace":"web","task":"  แก้ test\u0007 หน้า login\r\nบรรทัดสอง  "}`)
	if code != 200 {
		t.Fatalf("mission: %d %v", code, m)
	}
	calls := e.runner.agcmdCalls()
	want := []string{"eng", "แก้ test หน้า login\nบรรทัดสอง", "web"}
	if len(calls) != 1 || strings.Join(calls[0], "|") != strings.Join(want, "|") {
		t.Fatalf("agcmd argv = %q, want %q", calls, want)
	}
	if d, _ := m["details"].(map[string]any); d["runId"] != "ENG-20261104-001" {
		t.Fatalf("run id not returned: %v", m)
	}
	// audited: intent + result for the mission
	f, _ := os.Open(e.auditFP)
	defer f.Close()
	var events []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var r audit.Record
		_ = json.Unmarshal(sc.Bytes(), &r)
		if r.RPC == "aiagent.mission" {
			events = append(events, r.Event+":"+r.Result)
		}
	}
	if strings.Join(events, ",") != "intent:,result:error,intent:,result:ok" {
		t.Fatalf("audit trail = %v", events)
	}
}

func TestInputValidation(t *testing.T) {
	e := newEnv(t)
	tok, _ := e.store.Enable("test")
	e.runner.sessions = "web\t1\n"
	e.runner.panes = "web\tagent\t1\tcodex\n"
	cases := []string{
		`{"workspace":"deploy","task":"x"}`,
		`{"workspace":"../../etc","task":"x"}`,
		`{"workspace":"web","task":"   "}`,
		`{"workspace":"web","task":"status"}`,
		`{"workspace":"web","task":"--help"}`,
		`{"workspace":"web","task":"` + strings.Repeat("ก", maxTextRunes+1) + `"}`,
	}
	for _, body := range cases {
		time.Sleep(writeSpacing)
		if code, m, _ := e.do(t, "POST", "/aiagent/api/missions", tok, body); code != 400 {
			t.Fatalf("%s → %d %v, want 400", body[:min(60, len(body))], code, m)
		}
	}
	if n := len(e.runner.agcmdCalls()); n != 0 {
		t.Fatalf("invalid input reached agcmd %d times", n)
	}
	time.Sleep(writeSpacing)
	if code, _, _ := e.do(t, "POST", "/aiagent/api/runs/..%2Fx/approve", tok, ""); code != 400 && code != 404 {
		t.Fatalf("bad run id must be rejected: %d", code)
	}
}

func TestAnswerOnlyWhenWaiting(t *testing.T) {
	e := newEnv(t)
	tok, _ := e.store.Enable("test")
	e.runner.sessions = "api\t1\n"
	e.runner.panes = "api\tagent\t1\tnode\n"
	writeFile(t, filepath.Join(e.runs, "ENG-20261104-002.json"), `{"id":"ENG-20261104-002","task":"t","workspace":"api","session":"api","status":"RUNNING","created_at":"2026-11-04T01:00:00Z","updated_at":"2026-11-04T01:00:00Z","decision":null}`)
	if code, _, _ := e.do(t, "POST", "/aiagent/api/runs/ENG-20261104-002/answer", tok, `{"answer":"ok"}`); code != 409 {
		t.Fatalf("answer to a non-waiting run: %d", code)
	}
	writeFile(t, filepath.Join(e.runs, "ENG-20261104-002.json"), `{"id":"ENG-20261104-002","task":"t","workspace":"api","session":"api","status":"WAITING_DECISION","created_at":"2026-11-04T01:00:00Z","updated_at":"2026-11-04T01:00:00Z","decision":{"question":"merge?","options":["yes","no"]}}`)
	time.Sleep(writeSpacing)
	if code, m, _ := e.do(t, "POST", "/aiagent/api/runs/ENG-20261104-002/answer", tok, `{"answer":"yes"}`); code != 200 {
		t.Fatalf("answer: %d %v", code, m)
	}
	calls := e.runner.agcmdCalls()
	if len(calls) != 1 || strings.Join(calls[0], "|") != "eng|answer|ENG-20261104-002|yes" {
		t.Fatalf("agcmd argv = %q", calls)
	}
}

// ---------- collection ----------

func TestClaudeCollection(t *testing.T) {
	e := newEnv(t)
	fakeProc(t, e.proc, "4242", "777")
	fakeProc(t, e.proc, "5000", "999") // recycled pid: start time differs
	writeFile(t, filepath.Join(e.home, ".claude", "sessions", "4242.json"), `{"pid":4242,"sessionId":"s-live","cwd":"/home/dev/web","startedAt":1793750000000,"procStart":"777","name":"dev-01","status":"busy"}`)
	writeFile(t, filepath.Join(e.home, ".claude", "sessions", "5000.json"), `{"pid":5000,"sessionId":"s-stale","cwd":"/x","procStart":"111","status":"busy"}`)
	writeFile(t, filepath.Join(e.home, ".claude", "sessions", "4242.secret.key"), "SECRET")
	lines := []string{
		`{"type":"user","timestamp":"2026-11-04T03:00:00Z","cwd":"/home/dev/web","gitBranch":"feat/x","message":{"content":"hi"}}`,
		`{"type":"assistant","timestamp":"2026-11-04T03:00:01Z","message":{"id":"m1","usage":{"input_tokens":10,"output_tokens":5,"cache_creation_input_tokens":2,"cache_read_input_tokens":100},"content":[{"type":"text","text":"x"}]}}`,
		`{"type":"assistant","timestamp":"2026-11-04T03:00:02Z","message":{"id":"m1","usage":{"input_tokens":10,"output_tokens":5,"cache_creation_input_tokens":2,"cache_read_input_tokens":100},"content":[{"type":"tool_use","name":"Bash","input":{"command":"go test ./..."}}]}}`,
		`{"type":"assistant","timestamp":"2026-11-04T03:00:03Z","message":{"id":"m2","usage":{"input_tokens":1,"output_tokens":1},"content":[{"type":"tool_use","name":"Edit","input":{}}]}}`,
		`{"type":"ai-title","sessionId":"s-live","aiTitle":"Fix login tests"}`,
		`{"type":"pr-link","prNumber":7,"prUrl":"https://github.com/o/r/pull/7","prRepository":"o/r"}`,
		`{"type":"pr-link","prNumber":8,"prUrl":"javascript:alert(1)","prRepository":"o/r"}`,
	}
	writeFile(t, filepath.Join(e.home, ".claude", "projects", "-home-dev-web", "s-live.jsonl"), strings.Join(lines, "\n")+"\n")

	snap := e.col.Snapshot(context.Background())
	var live []Session
	for _, s := range snap.Sessions {
		if s.Live {
			live = append(live, s)
		}
	}
	if len(live) != 1 {
		t.Fatalf("want exactly the proven-alive session, got %+v", live)
	}
	s := live[0]
	if s.SessionID != "s-live" || s.Status != StatusRunning || s.ActivityType != ActivityCodeEdit || s.Workspace != "web" || s.GitBranch != "feat/x" || s.Title != "Fix login tests" {
		t.Fatalf("session = %+v", s)
	}
	if s.Usage.InputTokens != 13 || s.Usage.OutputTokens != 6 || s.Usage.CachedTokens != 100 || !s.Usage.Complete {
		t.Fatalf("usage (dup message id must count once) = %+v", s.Usage)
	}
	if len(s.PRs) != 1 || s.PRs[0].Number != 7 {
		t.Fatalf("only https PR links may pass: %+v", s.PRs)
	}
	b, _ := json.Marshal(snap)
	if strings.Contains(string(b), "go test") || strings.Contains(string(b), "SECRET") {
		t.Fatal("tool inputs / secrets must never reach the snapshot")
	}
}

func TestCodexCollectionAndQuota(t *testing.T) {
	e := newEnv(t)
	roll := filepath.Join(e.home, ".codex", "sessions", "2026", "11", "04", "rollout-x.jsonl")
	reset := e.now.Add(4 * time.Hour).Unix()
	lines := []string{
		`{"timestamp":"2026-11-04T03:00:00Z","type":"session_meta","payload":{"id":"cx-1","cwd":"/home/dev/api","timestamp":"2026-11-04T03:00:00Z","git":{"branch":"feat/y"}}}`,
		`{"timestamp":"2026-11-04T03:01:00Z","type":"event_msg","payload":{"type":"task_started"}}`,
		`{"timestamp":"2026-11-04T03:02:00Z","type":"response_item","payload":{"type":"custom_tool_call","name":"exec","input":"cd x && go test ./controllers"}}`,
		`{"timestamp":"2026-11-04T03:03:00Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":5000,"cached_input_tokens":4000,"output_tokens":300}},"rate_limits":{"primary":{"used_percent":66,"window_minutes":300,"resets_at":` + itoa(reset) + `},"plan_type":"pro"}}}`,
	}
	writeFile(t, roll, strings.Join(lines, "\n")+"\n")
	writeFile(t, filepath.Join(e.home, ".codex", "session_index.jsonl"), `{"id":"cx-1","thread_name":"API tests","updated_at":"x"}`+"\n")
	// a live process holds the rollout open
	if err := os.MkdirAll(filepath.Join(e.proc, "777", "fd"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(roll, filepath.Join(e.proc, "777", "fd", "9")); err != nil {
		t.Fatal(err)
	}
	snap := e.col.Snapshot(context.Background())
	if len(snap.Sessions) != 1 {
		t.Fatalf("sessions = %+v", snap.Sessions)
	}
	s := snap.Sessions[0]
	if !s.Live || s.LiveEvidence != "openFileHandle" || s.Status != StatusRunning || s.ActivityType != ActivityTestRun || s.Title != "API tests" || s.Workspace != "api" {
		t.Fatalf("codex session = %+v", s)
	}
	if s.Usage.InputTokens != 5000 || s.Usage.CachedTokens != 4000 || s.Usage.OutputTokens != 300 {
		t.Fatalf("usage = %+v", s.Usage)
	}
	var q *Quota
	for i := range snap.Quotas {
		if snap.Quotas[i].ProviderID == "openai" {
			q = &snap.Quotas[i]
		}
	}
	if q == nil || q.Confidence != "actual" || *q.RemainingPercent != 34 || q.Mode != "eco" {
		t.Fatalf("codex quota = %+v", q)
	}
	for _, x := range snap.Quotas {
		if x.ProviderID == "anthropic" && (x.Confidence != "unknown" || x.RemainingPercent != nil) {
			t.Fatalf("claude quota must be unknown, never estimated: %+v", x)
		}
	}
	// after the reset time passes, the old percentage must not be shown
	e.now = e.now.Add(5 * time.Hour)
	stale := codexQuotas(&codexReading{at: e.now.Add(-5 * time.Hour), limits: codexRateLimits{Primary: &codexWindow{UsedPercent: 66, WindowMinutes: 300, ResetsAt: reset}}}, e.now)
	if stale[0].Confidence != "stale" || stale[0].RemainingPercent != nil {
		t.Fatalf("stale quota = %+v", stale[0])
	}
}

func TestRunsLostWhenTmuxGone(t *testing.T) {
	e := newEnv(t)
	writeFile(t, filepath.Join(e.runs, "ENG-20261104-001.json"), `{"id":"ENG-20261104-001","task":"t","workspace":"api","session":"api","status":"RUNNING","created_at":"2026-11-04T01:00:00Z","updated_at":"2026-11-04T01:00:00Z"}`)
	writeFile(t, filepath.Join(e.runs, "ENG-20261104-003.json"), `{"id":"ENG-20261104-003","task":"old","workspace":"api","session":"api","status":"ARCHIVED"}`)
	snap := e.col.Snapshot(context.Background())
	if len(snap.Runs) != 1 || !snap.Runs[0].Lost || snap.Summary.Lost != 1 {
		t.Fatalf("runs = %+v summary = %+v", snap.Runs, snap.Summary)
	}
}

func TestDispatchViaRequiresPaneAncestry(t *testing.T) {
	e := newEnv(t)
	// pane shell 900 → claude 4242 (dispatchable); claude 4343 elsewhere in the same dir
	fakeProc(t, e.proc, "900", "1")
	fakeProcPPID(t, e.proc, "4242", "777", "900")
	fakeProcPPID(t, e.proc, "4343", "888", "1")
	writeFile(t, filepath.Join(e.home, ".claude", "sessions", "4242.json"), `{"pid":4242,"sessionId":"in-tmux","cwd":"/home/dev/docs","procStart":"777","status":"idle"}`)
	writeFile(t, filepath.Join(e.home, ".claude", "sessions", "4343.json"), `{"pid":4343,"sessionId":"elsewhere","cwd":"/home/dev/docs","procStart":"888","status":"idle"}`)
	e.runner.sessions = "docs\t1\n"
	e.runner.panes = "docs\tagent\t1\tclaude\t900\n"
	snap := e.col.Snapshot(context.Background())
	got := map[string]string{}
	for _, s := range snap.Sessions {
		got[s.SessionID] = s.DispatchVia
	}
	if got["in-tmux"] != "docs" || got["elsewhere"] != "" {
		t.Fatalf("dispatchVia = %v; only the pane's own process may be targeted", got)
	}
}

func TestDispatchViaAttachedBackgroundSession(t *testing.T) {
	e := newEnv(t)
	// tmux pane shell 900 → `claude attach 580c97ee` (pid 901); the bg session
	// itself (pid 5555) is NOT a descendant of the pane
	fakeProc(t, e.proc, "900", "1")
	fakeProcPPID(t, e.proc, "901", "2", "900")
	writeFile(t, filepath.Join(e.proc, "901", "cmdline"), "claude\x00attach\x00580c97ee\x00")
	fakeProcPPID(t, e.proc, "5555", "555", "1")
	writeFile(t, filepath.Join(e.home, ".claude", "sessions", "5555.json"), `{"pid":5555,"sessionId":"580c97ee-b316-478d-a728-62b9b8e37b3b","cwd":"/root","procStart":"555","status":"busy","name":"console"}`)
	writeFile(t, filepath.Join(e.home, ".claude", "sessions", "5556.json"), `{"pid":5556,"sessionId":"580c97ff-0000","cwd":"/root","procStart":"556","status":"idle"}`)
	fakeProcPPID(t, e.proc, "5556", "556", "1")
	e.runner.sessions = "docs\t1\n"
	e.runner.panes = "docs\tagent\t1\tclaude\t900\n"
	snap := e.col.Snapshot(context.Background())
	got := map[string]string{}
	for _, s := range snap.Sessions {
		got[s.SessionID] = s.DispatchVia
	}
	if got["580c97ee-b316-478d-a728-62b9b8e37b3b"] != "docs" || got["580c97ff-0000"] != "" {
		t.Fatalf("dispatchVia = %v; the attached session (and only it) is the target", got)
	}
}

func TestStopUsesOnlySupportedPaths(t *testing.T) {
	e := newEnv(t)
	tok, _ := e.store.Enable("test")
	fakeProcPPID(t, e.proc, "5001", "51", "1")
	fakeProcPPID(t, e.proc, "5002", "52", "1")
	fakeProc(t, e.proc, "900", "1")
	fakeProcPPID(t, e.proc, "5003", "53", "900")
	writeFile(t, filepath.Join(e.home, ".claude", "sessions", "5001.json"), `{"pid":5001,"sessionId":"aaaa1111-0000-0000-0000-000000000000","cwd":"/root","procStart":"51","kind":"bg","status":"idle"}`)
	writeFile(t, filepath.Join(e.home, ".claude", "sessions", "5002.json"), `{"pid":5002,"sessionId":"bbbb2222-0000-0000-0000-000000000000","cwd":"/root","procStart":"52","kind":"interactive","status":"idle"}`)
	writeFile(t, filepath.Join(e.home, ".claude", "sessions", "5003.json"), `{"pid":5003,"sessionId":"cccc3333-0000-0000-0000-000000000000","cwd":"/home/dev/docs","procStart":"53","kind":"interactive","status":"busy"}`)
	e.runner.sessions = "docs\t1\n"
	e.runner.panes = "docs\tagent\t1\tclaude\t900\n"

	if code, m, _ := e.do(t, "POST", "/aiagent/api/sessions/bbbb2222-0000-0000-0000-000000000000/stop", tok, ""); code != 409 {
		t.Fatalf("interactive session must be refused: %d %v", code, m)
	}
	time.Sleep(writeSpacing)
	if code, m, _ := e.do(t, "POST", "/aiagent/api/sessions/aaaa1111-0000-0000-0000-000000000000/stop", tok, ""); code != 200 {
		t.Fatalf("own bg session: %d %v", code, m)
	}
	time.Sleep(writeSpacing)
	if code, m, _ := e.do(t, "POST", "/aiagent/api/sessions/cccc3333-0000-0000-0000-000000000000/stop", tok, ""); code != 200 {
		t.Fatalf("pane session: %d %v", code, m)
	}
	time.Sleep(writeSpacing)
	if code, _, _ := e.do(t, "POST", "/aiagent/api/sessions/does-not-exist/stop", tok, ""); code != 404 {
		t.Fatalf("unknown session: %d", code)
	}
	var stops [][]string
	for _, c := range e.runner.callsTo("claude") {
		if c[0] == "stop" {
			stops = append(stops, c)
		}
	}
	if got := stops; len(got) != 1 || strings.Join(got[0], " ") != "stop aaaa1111" {
		t.Fatalf("claude calls = %q", got)
	}
	if got := e.runner.agcmdCalls(); len(got) != 1 || strings.Join(got[0], " ") != "restart docs" {
		t.Fatalf("agcmd calls = %q", got)
	}
}

func TestSessionMessageRelayAndConversation(t *testing.T) {
	e := newEnv(t)
	tok, _ := e.store.Enable("test")
	fakeProcPPID(t, e.proc, "5001", "51", "1")
	fakeProcPPID(t, e.proc, "5002", "52", "1")
	writeFile(t, filepath.Join(e.home, ".claude", "sessions", "5001.json"), `{"pid":5001,"sessionId":"aaaa1111-0000-0000-0000-000000000000","cwd":"/root","procStart":"51","kind":"bg","status":"idle"}`)
	writeFile(t, filepath.Join(e.home, ".claude", "sessions", "5002.json"), `{"pid":5002,"sessionId":"bbbb2222-0000-0000-0000-000000000000","cwd":"/root","procStart":"52","kind":"interactive","status":"idle"}`)
	lines := []string{
		`{"type":"user","timestamp":"2026-11-04T03:00:00Z","message":{"role":"user","content":"ใช้ token kaw_FAKEfakeFAKEfakeFAKEfakeFAKEfake00000000 กับ ghp_abcdefghijklmnopqrstuvwxyz0123 และ ABCD-EFGH-JKLM-NPQR-STUV-WXYZ-2345-6789<system-reminder>hidden</system-reminder>"}}`,
		`{"type":"assistant","timestamp":"2026-11-04T03:00:01Z","message":{"id":"m1","role":"assistant","content":[{"type":"thinking","thinking":"secret thoughts"},{"type":"text","text":"รับทราบ"}]}}`,
		`{"type":"assistant","timestamp":"2026-11-04T03:00:01Z","message":{"id":"m1","role":"assistant","content":[{"type":"text","text":"กำลังเริ่ม"}]}}`,
		`{"type":"assistant","timestamp":"2026-11-04T03:00:02Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"cat /etc/shadow"}}]}}`,
		`{"type":"user","timestamp":"2026-11-04T03:00:03Z","message":{"role":"user","content":[{"type":"tool_result","content":"root:x:0"}]}}`,
		`{"type":"assistant","timestamp":"2026-11-04T03:00:04Z","message":{"role":"assistant","content":[{"type":"text","text":"เสร็จแล้ว password=hunter2secret"}],"id":"m2"}}`,
		`{"type":"user","timestamp":"2026-11-04T03:00:05Z","message":{"role":"user","content":"<bash-input>ls /tmp</bash-input>"}}`,
		`{"type":"user","timestamp":"2026-11-04T03:00:06Z","message":{"role":"user","content":"<bash-stdout>topsecret-output</bash-stdout><bash-stderr></bash-stderr>"}}`,
		`{"type":"user","timestamp":"2026-11-04T03:00:07Z","message":{"role":"user","content":"<task-notification><task-id>x</task-id></task-notification>"}}`,
	}
	writeFile(t, filepath.Join(e.home, ".claude", "projects", "-root", "aaaa1111-0000-0000-0000-000000000000.jsonl"), strings.Join(lines, "\n")+"\n")

	code, m, body := e.do(t, "GET", "/aiagent/api/sessions/aaaa1111-0000-0000-0000-000000000000/messages", tok, "")
	if code != 200 {
		t.Fatalf("messages: %d %v", code, m)
	}
	for _, leak := range []string{"kaw_FAKE", "ghp_abc", "ABCD-EFGH", "hidden", "secret thoughts", "/etc/shadow", "root:x:0", "hunter2", "topsecret-output", "task-notification", "bash-input"} {
		if strings.Contains(body, leak) {
			t.Fatalf("conversation leaked %q: %s", leak, body)
		}
	}
	msgs := m["details"].(map[string]any)["messages"].([]any)
	// one streamed message (same id) merges; a later progress note stays separate
	if len(msgs) != 4 || msgs[0].(map[string]any)["role"] != "user" || msgs[1].(map[string]any)["text"] != "รับทราบ\n\nกำลังเริ่ม" ||
		!strings.HasPrefix(msgs[2].(map[string]any)["text"].(string), "เสร็จแล้ว") || msgs[3].(map[string]any)["text"] != "! ls /tmp" {
		t.Fatalf("messages = %v", msgs)
	}

	if code, _, _ := e.do(t, "POST", "/aiagent/api/sessions/bbbb2222-0000-0000-0000-000000000000/message", tok, `{"message":"hi"}`); code != 409 {
		t.Fatalf("interactive session must stay read-only: %d", code)
	}
	time.Sleep(writeSpacing)
	if code, m, _ := e.do(t, "POST", "/aiagent/api/sessions/aaaa1111-0000-0000-0000-000000000000/message", tok, `{"message":"สวัสดีจากเว็บ"}`); code != 200 {
		t.Fatalf("relay: %d %v", code, m)
	}
	var got []string
	for _, c := range e.runner.callsTo("tmux") {
		if c[0] == "new-session" || c[0] == "send-keys" || c[0] == "set-option" || c[0] == "resize-window" {
			got = append(got, strings.Join(c, " "))
		}
	}
	// Every pane-targeting command takes "=name:", never "=name". This list
	// used to hold the bare form for the two send-keys lines, which is what
	// the code did and what real tmux rejects — so the test was pinning the
	// bug in place: no relayed message had ever reached an agent.
	want := []string{
		"new-session -d -s aiagent-aaaa1111 -x 220 -y 50 claude attach aaaa1111",
		"set-option -w -t =aiagent-aaaa1111: window-size manual",
		"resize-window -t =aiagent-aaaa1111: -x 220 -y 50",
		"send-keys -t =aiagent-aaaa1111: -l -- สวัสดีจากเว็บ",
		"send-keys -t =aiagent-aaaa1111: Enter",
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("relay tmux calls =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestDaemonStatesAndParkedSessions(t *testing.T) {
	e := newEnv(t)
	fakeProcPPID(t, e.proc, "5001", "51", "1")
	writeFile(t, filepath.Join(e.home, ".claude", "sessions", "5001.json"), `{"pid":5001,"sessionId":"aaaa1111-0000-0000-0000-000000000000","cwd":"/root","procStart":"51","kind":"bg","status":"busy"}`)
	e.runner.agentsJSON = `[
	 {"id":"aaaa1111","sessionId":"aaaa1111-0000-0000-0000-000000000000","name":"live one","kind":"background","state":"blocked"},
	 {"id":"cccc3333","sessionId":"cccc3333-0000-0000-0000-000000000000","name":"parked","kind":"background","state":"blocked","cwd":"/home/dev/web"},
	 {"id":"dddd4444","sessionId":"dddd4444-0000-0000-0000-000000000000","name":"finished","kind":"background","state":"done"},
	 {"id":null,"sessionId":"eeee","name":"term","kind":"interactive","state":null}]`
	snap := e.col.Snapshot(context.Background())
	by := map[string]Session{}
	for _, s := range snap.Sessions {
		by[s.SessionID] = s
	}
	if s := by["aaaa1111-0000-0000-0000-000000000000"]; s.Status != StatusNeedsInput || s.DisplayName != "live one" || s.MessageVia != "relay" {
		t.Fatalf("live bg = %+v", s)
	}
	if s := by["cccc3333-0000-0000-0000-000000000000"]; !s.Live || s.Status != StatusNeedsInput || s.LiveEvidence != "claudeDaemon" || s.MessageVia != "relay" || s.Workspace != "web" {
		t.Fatalf("parked bg = %+v", s)
	}
	if s := by["dddd4444-0000-0000-0000-000000000000"]; s.Status != StatusCompleted {
		t.Fatalf("done bg = %+v", s)
	}
	if _, ok := by["eeee"]; ok {
		t.Fatal("interactive entries come from the pid registry, not the daemon list")
	}
	if snap.Summary.NeedsInput != 2 || snap.Summary.Completed != 1 {
		t.Fatalf("summary = %+v", snap.Summary)
	}
}

func TestUploadOnlyImages(t *testing.T) {
	e := newEnv(t)
	tok, _ := e.store.Enable("test")
	png := "\x89PNG\r\n\x1a\n" + strings.Repeat("x", 64)
	req := httptest.NewRequest("POST", "/aiagent/api/uploads", strings.NewReader(png))
	req.Header.Set(TokenHeader, tok)
	req.Header.Set("Content-Type", "image/png")
	resp, err := e.app.Test(req)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("png upload: %v %v", resp.StatusCode, err)
	}
	var m map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&m)
	p := m["details"].(map[string]any)["path"].(string)
	fi, err := os.Stat(p)
	if err != nil || fi.Mode().Perm() != 0o600 || !strings.HasSuffix(p, ".png") || !strings.HasPrefix(p, filepath.Join(e.store.dir, "uploads")) {
		t.Fatalf("stored upload %q %v %v", p, fi, err)
	}
	for _, bad := range []string{"#!/bin/sh\nrm -rf /", "<svg onload=alert(1)>", ""} {
		req := httptest.NewRequest("POST", "/aiagent/api/uploads", strings.NewReader(bad))
		req.Header.Set(TokenHeader, tok)
		resp, _ := e.app.Test(req)
		if resp.StatusCode != 400 {
			t.Fatalf("non-image %q accepted: %d", bad, resp.StatusCode)
		}
	}
	get := httptest.NewRequest("GET", "/aiagent/api/uploads/"+filepath.Base(p), nil)
	get.Header.Set(TokenHeader, tok)
	if resp, _ := e.app.Test(get); resp.StatusCode != 200 {
		t.Fatalf("read back upload: %d", resp.StatusCode)
	}
	for _, bad := range []string{"..%2Fstate.json", "state.json", filepath.Base(p) + ".sh"} {
		g := httptest.NewRequest("GET", "/aiagent/api/uploads/"+bad, nil)
		g.Header.Set(TokenHeader, tok)
		if resp, _ := e.app.Test(g); resp.StatusCode != 404 {
			t.Fatalf("upload read %q must 404: %d", bad, resp.StatusCode)
		}
	}
	req = httptest.NewRequest("POST", "/aiagent/api/uploads", strings.NewReader(png))
	if resp, _ := e.app.Test(req); resp.StatusCode != 401 {
		t.Fatalf("upload without token: %d", resp.StatusCode)
	}
}

func TestCodexPresenceWithoutThread(t *testing.T) {
	e := newEnv(t)
	writeFile(t, filepath.Join(e.proc, "7001", "cmdline"), "/root/.vscode-server/extensions/openai.chatgpt-1/bin/codex\x00app-server\x00")
	if err := os.Symlink("/root", filepath.Join(e.proc, "7001", "cwd")); err != nil {
		t.Fatal(err)
	}
	snap := e.col.Snapshot(context.Background())
	var found *Session
	for i := range snap.Sessions {
		if snap.Sessions[i].SessionID == "codex-proc-7001" {
			found = &snap.Sessions[i]
		}
	}
	if found == nil || found.DisplayName != "Codex · VS Code" || !found.Live || found.MessageVia != "" || found.StopVia != "" {
		t.Fatalf("codex presence = %+v", found)
	}
}

func TestSubAgentsListed(t *testing.T) {
	e := newEnv(t)
	e.now = time.Now()
	fakeProc(t, e.proc, "4242", "777")
	writeFile(t, filepath.Join(e.home, ".claude", "sessions", "4242.json"), `{"pid":4242,"sessionId":"s-par","cwd":"/root","procStart":"777","status":"busy"}`)
	base := filepath.Join(e.home, ".claude", "projects", "-root")
	writeFile(t, filepath.Join(base, "s-par.jsonl"), `{"type":"user","timestamp":"2026-11-04T03:00:00Z","message":{"content":"go"}}`+"\n")
	sub := filepath.Join(base, "s-par", "subagents")
	writeFile(t, filepath.Join(sub, "agent-a1.meta.json"), `{"agentType":"Explore","description":"Audit gateway token kaw_FAKEfakeFAKEfakeFAKEfakeFAKEfake00000000","finished":false}`)
	writeFile(t, filepath.Join(sub, "agent-a1.jsonl"), `{"type":"user","isSidechain":true,"timestamp":"2026-11-04T03:00:00Z","message":{"role":"user","content":"Find every caller of the gateway token api_key=supersecret99"}}`+"\n"+
		`{"type":"assistant","isSidechain":true,"timestamp":"2026-11-04T03:00:01Z","message":{"id":"m","content":[{"type":"text","text":"Found 3 callers so far"},{"type":"tool_use","name":"Grep","input":{}}]}}`+"\n")
	writeFile(t, filepath.Join(sub, "agent-a2.meta.json"), `{"agentType":"general-purpose","description":"old done","finished":true}`)
	writeFile(t, filepath.Join(sub, "agent-a2.jsonl"), "{}\n")
	old := time.Now().Add(-time.Hour)
	_ = os.Chtimes(filepath.Join(sub, "agent-a2.jsonl"), old, old)
	snap := e.col.Snapshot(context.Background())
	var subs []SubAgent
	for _, s := range snap.Sessions {
		if s.SessionID == "s-par" {
			subs = s.SubAgents
		}
	}
	if len(subs) != 1 || subs[0].ID != "a1" || subs[0].Status != "running" || subs[0].ActivityType != ActivityResearch || strings.Contains(subs[0].Description, "kaw_") ||
		!strings.HasPrefix(subs[0].Task, "Find every caller") || strings.Contains(subs[0].Task, "supersecret99") || subs[0].Latest != "Found 3 callers so far" {
		t.Fatalf("subagents = %+v", subs)
	}
}

// TestEveryEmbeddedAssetIsServable guards the asset allowlist against the
// files actually shipped (a too-strict pattern silently 404s sprites).
func TestEveryEmbeddedAssetIsServable(t *testing.T) {
	ents, err := fs.ReadDir(uiFS, "assets")
	if err != nil || len(ents) == 0 {
		t.Fatalf("no embedded assets: %v", err)
	}
	for _, e := range ents {
		if !assetName.MatchString(e.Name()) {
			t.Errorf("asset %q does not match the serving allowlist", e.Name())
		}
	}
}

func fakeProcPPID(t *testing.T, root, pid, start, ppid string) {
	t.Helper()
	fakeProc(t, root, pid, start)
	p := filepath.Join(root, pid, "stat")
	b, _ := os.ReadFile(p)
	f := strings.Fields(string(b))
	f[3] = ppid // pid (comm) state ppid ...
	writeFile(t, p, strings.Join(f, " ")+"\n")
}

// ---------- helpers & invariants ----------

func TestQuotaModeBands(t *testing.T) {
	for rem, want := range map[float64]string{100: "normal", 61: "normal", 60: "eco", 30: "eco", 29.9: "critical", 10: "critical", 9.9: "reserve", 0: "reserve"} {
		if got := quotaMode(rem); got != want {
			t.Errorf("quotaMode(%v) = %s, want %s", rem, got, want)
		}
	}
}

func TestClassify(t *testing.T) {
	cases := map[[2]string]string{
		{"Edit", ""}:                  ActivityCodeEdit,
		{"Read", ""}:                  ActivityResearch,
		{"Bash", "go test ./..."}:     ActivityTestRun,
		{"Bash", "kubectl rollout"}:   ActivityDeploy,
		{"exec", "apply_patch <<EOF"}: ActivityCodeEdit,
		{"Bash", "make build"}:        ActivityCommand,
		{"Agent", ""}:                 ActivityPlanning,
	}
	for in, want := range cases {
		if got := classify(in[0], in[1]); got != want {
			t.Errorf("classify(%q) = %s, want %s", in, got, want)
		}
	}
}

// The old lock-step test read a private dispatch script and kept a
// compiled-in table in lock-step with one company's private script. Both the
// table and that script are gone; what replaces it is the config loader and
// the lookups below.

// TestLoadWorkspaces covers the config path that replaced the compiled-in
// table: a good file, a missing file (view-only, not an error), and each way a
// bad entry must be refused at startup rather than at dispatch time.
func TestLoadWorkspaces(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		writeFile(t, p, body)
		return p
	}

	good := write("good.json", `{"workspaces":[
		{"name":"web","tmuxSession":"web","dir":"/home/dev/web/"},
		{"name":"api","tmuxSession":"api-agent","dir":"/home/dev/api"}
	]}`)
	ws, err := LoadWorkspaces(good)
	if err != nil {
		t.Fatalf("good config: %v", err)
	}
	if len(ws) != 2 {
		t.Fatalf("want 2 workspaces, got %d", len(ws))
	}
	// The trailing slash must be cleaned, or prefix matching in workspaceFor
	// would compare against "/home/dev/web//".
	if ws[0] != (workspaceEntry{name: "web", session: "web", dir: "/home/dev/web"}) {
		t.Errorf("first entry = %+v", ws[0])
	}
	if ws[1].session != "api-agent" {
		t.Errorf("tmuxSession not read: %+v", ws[1])
	}

	// A missing file means "no workspaces", which is a valid view-only install.
	if ws, err := LoadWorkspaces(filepath.Join(dir, "absent.json")); err != nil || ws != nil {
		t.Errorf("missing file: got %v, %v; want nil, nil", ws, err)
	}
	if ws, err := LoadWorkspaces(""); err != nil || ws != nil {
		t.Errorf("empty path: got %v, %v; want nil, nil", ws, err)
	}

	for _, bad := range []struct{ name, body string }{
		{"no name", `{"workspaces":[{"tmuxSession":"s","dir":"/a"}]}`},
		{"no session", `{"workspaces":[{"name":"w","dir":"/a"}]}`},
		{"no dir", `{"workspaces":[{"name":"w","tmuxSession":"s"}]}`},
		{"relative dir", `{"workspaces":[{"name":"w","tmuxSession":"s","dir":"rel/path"}]}`},
		{"duplicate name", `{"workspaces":[{"name":"w","tmuxSession":"s","dir":"/a"},{"name":"w","tmuxSession":"t","dir":"/b"}]}`},
		{"not json", `{`},
	} {
		if _, err := LoadWorkspaces(write("bad.json", bad.body)); err == nil {
			t.Errorf("%s: accepted, want an error", bad.name)
		}
	}
}

// TestWorkspaceLookups checks the two things the config feeds: resolving a
// name the browser sent, and naming the workspace a session's cwd sits in.
// The nested case is the one that matters — a checkout inside another
// workspace must report itself, not its parent.
func TestWorkspaceLookups(t *testing.T) {
	c := &Collector{Workspaces: []workspaceEntry{
		{name: "api", session: "api", dir: "/home/dev/api"},
		{name: "apiDocs", session: "api-docs", dir: "/home/dev/api/docs"},
	}}

	if w, ok := c.lookupWorkspace("api"); !ok || w.session != "api" {
		t.Errorf(`lookupWorkspace("api") = %+v, %v`, w, ok)
	}
	if _, ok := c.lookupWorkspace("nope"); ok {
		t.Error("an unconfigured name must not resolve")
	}

	for cwd, want := range map[string]string{
		"/home/dev/api":            "api",
		"/home/dev/api/cmd/server": "api",
		"/home/dev/api/docs":       "apiDocs", // longest match wins
		"/home/dev/api/docs/ref":   "apiDocs",
		"/home/dev/apiary":         "", // prefix of a name, but not inside it
		"/elsewhere":               "",
	} {
		if got := c.workspaceFor(cwd); got != want {
			t.Errorf("workspaceFor(%q) = %q, want %q", cwd, got, want)
		}
	}

	// With no workspaces configured nothing resolves and nothing classifies.
	empty := &Collector{}
	if _, ok := empty.lookupWorkspace("api"); ok {
		t.Error("empty config must resolve nothing")
	}
	if got := empty.workspaceFor("/home/dev/api"); got != "" {
		t.Errorf("empty config classified %q", got)
	}
}

func itoa(n int64) string {
	b, _ := json.Marshal(n)
	return string(b)
}

var _ = http.MethodGet

// TestMonitorWatches follows a Jenkins watch from start to end and checks the
// watch command (which can carry credentials) never reaches the API.
func TestMonitorWatches(t *testing.T) {
	e := newEnv(t)
	e.now = time.Date(2026, 11, 4, 7, 0, 0, 0, time.UTC)
	fakeProc(t, e.proc, "4343", "888")
	writeFile(t, filepath.Join(e.home, ".claude", "sessions", "4343.json"), `{"pid":4343,"sessionId":"s-ci","cwd":"/root","procStart":"888","status":"idle"}`)
	tr := filepath.Join(e.home, ".claude", "projects", "-root", "s-ci.jsonl")
	start := `{"type":"assistant","timestamp":"2026-11-04T06:59:10Z","message":{"id":"m1","content":[{"type":"tool_use","id":"tu1","name":"Monitor","input":{"description":"prod-gw-api 3.121.1 (queue 6164)","timeout_ms":1800000,"command":"A=admin:FAKEpass123; curl -u $A https://ci.example/queue/item/6164/api/json"}}]}}` + "\n" +
		`{"type":"user","timestamp":"2026-11-04T06:59:11Z","toolUseResult":{"taskId":"bt1","timeoutMs":1800000},"message":{"role":"user","content":[{"tool_use_id":"tu1","type":"tool_result","content":"Monitor started (task bt1)"}]}}` + "\n" +
		`{"type":"user","timestamp":"2026-11-04T06:59:41Z","message":{"role":"user","content":"<task-notification> <task-id>bt1</task-id> <summary>Monitor event</summary> <event>prod-gw-api started #55</event></task-notification>"}}` + "\n"
	writeFile(t, tr, start)
	find := func() *Session {
		e.col.invalidate()
		for _, s := range e.col.Snapshot(context.Background()).Sessions {
			if s.SessionID == "s-ci" {
				return &s
			}
		}
		t.Fatal("session missing")
		return nil
	}
	s := find()
	if len(s.Watches) != 1 || !s.Watches[0].CI || s.Watches[0].Label != "prod-gw-api 3.121.1 (queue 6164)" || s.Watches[0].Event != "prod-gw-api started #55" {
		t.Fatalf("watches = %+v", s.Watches)
	}
	b, _ := json.Marshal(s)
	if strings.Contains(string(b), "FAKEpass123") || strings.Contains(string(b), "curl") {
		t.Fatalf("watch command leaked: %s", b)
	}
	writeFile(t, tr, start+`{"type":"user","timestamp":"2026-11-04T07:02:11Z","message":{"role":"user","content":"<task-notification> <task-id>bt1</task-id> <status>completed</status> <event>prod-gw-api #55 =&gt; SUCCESS</event></task-notification>"}}`+"\n")
	if s = find(); len(s.Watches) != 0 {
		t.Fatalf("finished watch still active: %+v", s.Watches)
	}
	if s.LastCI == nil || s.LastCI.OK == nil || !*s.LastCI.OK || s.LastCI.Event != "prod-gw-api #55 => SUCCESS" {
		t.Fatalf("lastCI = %+v", s.LastCI)
	}
}

// TestStatuslineUsageAndQuota: the status line command keeps only the plan
// windows and model, and the collector turns them into actual/stale quotas.
func TestStatuslineUsageAndQuota(t *testing.T) {
	e := newEnv(t)
	now := time.Date(2026, 11, 4, 8, 0, 0, 0, time.UTC)
	e.now = now
	in := `{"session_id":"x","cwd":"/secret/path","cost":{"total_cost_usd":9},"model":{"id":"claude-opus-5-5","display_name":"Opus 5.5"},"rate_limits":{"five_hour":{"used_percentage":82,"resets_at":` + fmt.Sprint(now.Add(time.Hour).Unix()) + `},"seven_day":{"used_percentage":40,"resets_at":"2026-11-03T00:00:00Z"}}}`
	var out strings.Builder
	if err := RunStatusline(strings.NewReader(in), &out, e.home, now); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out.String()) != "Opus 5.5 · 5h 82% · 7d 40%" {
		t.Fatalf("status line = %q", out.String())
	}
	b, _ := os.ReadFile(UsagePath(e.home))
	if strings.Contains(string(b), "secret") || strings.Contains(string(b), "cost") {
		t.Fatalf("usage file keeps too much: %s", b)
	}
	qs := e.col.claudeQuotas(now)
	if len(qs) != 2 || qs[0].Confidence != "actual" || *qs[0].UsedPercent != 82 || qs[0].Mode != "critical" || qs[1].Confidence != "stale" || qs[1].UsedPercent != nil {
		t.Fatalf("quotas = %+v", qs)
	}
}

// TestSetModelAllowlist: only fixed aliases reach the session, as /model.
func TestSetModelAllowlist(t *testing.T) {
	e := newEnv(t)
	if _, err := e.col.SetModel(context.Background(), "any", "opus; rm -rf /"); err == nil || !strings.Contains(err.Error(), "model must be one of") {
		t.Fatalf("bad alias accepted: %v", err)
	}
	if _, err := e.col.SetModel(context.Background(), "nope", "sonnet"); err == nil {
		t.Fatal("unknown session accepted")
	}
}

// TestJobDoing reads a bg job's live status line, redacted, only while fresh.
func TestJobDoing(t *testing.T) {
	e := newEnv(t)
	e.now = time.Now()
	s := &Session{SessionID: "abcd1234-0000", Kind: "bg", home: e.home}
	p := filepath.Join(e.home, ".claude", "jobs", "abcd1234", "timeline.jsonl")
	writeFile(t, p, `{"at":"2026-11-04T08:00:00Z","state":"working","detail":"old"}`+"\n"+`{"at":"2026-11-04T08:01:00Z","state":"working","detail":"deploying with token kaw_FAKEfakeFAKEfakeFAKEfakeFAKEfake00000000"}`+"\n")
	d := e.col.jobDoing(s)
	if d == nil || !strings.HasPrefix(d.Detail, "deploying with token «") || strings.Contains(d.Detail, "kaw_") {
		t.Fatalf("doing = %+v", d)
	}
	old := time.Now().Add(-time.Hour)
	_ = os.Chtimes(p, old, old)
	if e.col.jobDoing(s) != nil {
		t.Fatal("stale job status shown")
	}
}

// TestReviewFlow: file a page, list it, view it once through a ticket in a
// locked-down sandbox, and answer it (copy-only when it has no session).
func TestReviewFlow(t *testing.T) {
	e := newEnv(t)
	e.now = time.Now()
	tok, _ := e.store.Enable("test")
	r, err := FileReview(e.home, "แก้หน้า policy", "สรุป", "", "codexCli", "/w", []byte("<h1>done</h1><script>alert(1)</script>"), e.now)
	if err != nil {
		t.Fatal(err)
	}
	code, m, _ := e.do(t, "GET", "/aiagent/api/reviews", tok, "")
	if code != 200 || len(m["details"].(map[string]any)["reviews"].([]any)) != 1 {
		t.Fatalf("list: %d %v", code, m)
	}
	if code, _, _ := e.do(t, "POST", "/aiagent/api/reviews/../../etc/view", tok, ""); code == 200 {
		t.Fatal("path id accepted")
	}
	code, m, _ = e.do(t, "POST", "/aiagent/api/reviews/"+r.ID+"/view", tok, "")
	if code != 200 {
		t.Fatalf("ticket: %d %v", code, m)
	}
	url := m["details"].(map[string]any)["url"].(string)
	req := httptest.NewRequest("GET", url, nil)
	resp, _ := e.app.Test(req)
	b, _ := io.ReadAll(resp.Body)
	csp := resp.Header.Get("Content-Security-Policy")
	if resp.StatusCode != 200 || !strings.Contains(string(b), "<h1>done</h1>") || !strings.HasPrefix(csp, "sandbox allow-scripts") || strings.Contains(csp, "allow-same-origin") || !strings.Contains(csp, "default-src 'none'") {
		t.Fatalf("view: %d csp=%q url=%s body=%s", resp.StatusCode, csp, url, b)
	}
	resp, _ = e.app.Test(httptest.NewRequest("GET", url, nil))
	if resp.StatusCode != 404 {
		t.Fatalf("ticket reused: %d", resp.StatusCode)
	}
	if code, _, _ := e.do(t, "POST", "/aiagent/api/reviews/"+r.ID+"/decision", tok, `{"decision":"revise","note":""}`); code != 400 {
		t.Fatalf("revise without note: %d", code)
	}
	e.svc.lastWrite = time.Time{}
	if code, _, _ := e.do(t, "POST", "/aiagent/api/reviews/"+r.ID+"/decision", tok, `{"decision":"approve","send":true}`); code != 409 {
		t.Fatalf("send without session: %d", code)
	}
	e.svc.lastWrite = time.Time{}
	code, m, _ = e.do(t, "POST", "/aiagent/api/reviews/"+r.ID+"/decision", tok, `{"decision":"revise","note":"ปุ่มเล็กไป"}`)
	reply, _ := m["details"].(map[string]any)["reply"].(string)
	if code != 200 || !strings.Contains(reply, "ขอแก้ไขเพิ่มเติม") || !strings.Contains(reply, "ปุ่มเล็กไป") || !strings.Contains(reply, r.ID) {
		t.Fatalf("decision: %d %q", code, reply)
	}
	if got := e.col.Reviews(); len(got) != 1 || got[0].Status != "revise" {
		t.Fatalf("status = %+v", got)
	}
	if _, _, err := e.col.DecideReview(context.Background(), r.ID, "approve", "", true); err == nil || !strings.Contains(err.Error(), "already answered") {
		t.Fatalf("second send accepted: %v", err)
	}
}

// TestAccountsFlow: status parsing, login argv, and code validation.
func TestAccountsFlow(t *testing.T) {
	e := newEnv(t)
	e.col.CodexPath = "codex"
	e.runner.agcmdOut = `{"loggedIn":true,"authMethod":"claude.ai","email":"a@b.co","orgName":"Org"}`
	acc := e.col.claudeAccount(context.Background())
	if !acc.LoggedIn || acc.Email != "a@b.co" || acc.Method != "claude.ai" {
		t.Fatalf("claude account = %+v", acc)
	}
	if _, err := e.col.StartLogin(context.Background(), "claude", "bad email; x"); err == nil {
		t.Fatal("bad email accepted")
	}
	if _, err := e.col.StartLogin(context.Background(), "rm", ""); err == nil {
		t.Fatal("bad provider accepted")
	}
	if _, err := e.col.StartLogin(context.Background(), "claude", "me@example.com"); err != nil {
		t.Fatal(err)
	}
	var argv string
	for _, c := range e.runner.callsTo("tmux") {
		if c[0] == "new-session" {
			argv = strings.Join(c, " ")
		}
	}
	if argv != "new-session -d -s aiagent-login-claude -x 220 -y 50 claude auth login --claudeai --email me@example.com" {
		t.Fatalf("login argv = %q", argv)
	}
	if err := e.col.SubmitLoginCode(context.Background(), "abc; rm -rf /"); err == nil {
		t.Fatal("bad code accepted")
	}
}

// TestUsageAfterAccountSwitch: a reading older than the credentials file is
// the previous account's and must not be shown.
func TestUsageAfterAccountSwitch(t *testing.T) {
	e := newEnv(t)
	now := time.Now()
	in := `{"rate_limits":{"seven_day":{"used_percentage":91}}}`
	if err := RunStatusline(strings.NewReader(in), io.Discard, e.home, now.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(e.home, ".claude", ".credentials.json"), "{}")
	if qs := e.col.claudeQuotas(now); len(qs) != 1 || qs[0].Confidence != "unknown" {
		t.Fatalf("old account reading shown: %+v", qs)
	}
	if err := e.col.SubmitLoginCode(context.Background(), "abc_DEF-123#state_x"); err == nil || strings.Contains(err.Error(), "does not look like") {
		t.Fatalf("underscore code rejected: %v", err)
	}
}

// TestPolicyDowngradeAndRestore: over the threshold a reachable Claude
// session gets the economy model and Codex's default changes; below the
// restore level both go back.
func TestPolicyDowngradeAndRestore(t *testing.T) {
	e := newEnv(t)
	e.now = time.Now()
	fakeProcPPID(t, e.proc, "5001", "51", "1")
	writeFile(t, filepath.Join(e.home, ".claude", "sessions", "5001.json"), `{"pid":5001,"sessionId":"aaaa1111-0000-0000-0000-000000000000","cwd":"/root","procStart":"51","kind":"bg","status":"idle"}`)
	writeFile(t, filepath.Join(e.home, ".claude", "projects", "-root", "aaaa1111-0000-0000-0000-000000000000.jsonl"),
		`{"type":"assistant","timestamp":"2026-11-04T03:00:01Z","message":{"id":"m1","model":"claude-opus-5-5","role":"assistant","content":[{"type":"text","text":"hi"}]}}`+"\n")
	cfg := filepath.Join(e.home, ".codex", "config.toml")
	writeFile(t, cfg, "model = \"gpt-6.1-sol\"\nmodel_reasoning_effort = \"medium\"\n\n[profiles.x]\nmodel = \"keep-me\"\n")
	usage := func(p float64) {
		in := fmt.Sprintf(`{"rate_limits":{"seven_day":{"used_percentage":%v}}}`, p)
		if err := RunStatusline(strings.NewReader(in), io.Discard, e.home, time.Now()); err != nil {
			t.Fatal(err)
		}
		// codex reading: reuse the quota path by faking an actual openai row is
		// not possible here, so Codex is exercised through setCodexModel below
		e.col.invalidate()
	}
	if _, err := e.svc.SetPolicy(true, 80, "claude-haiku-5-5", "gpt-6-luna"); err != nil {
		t.Fatal(err)
	}
	usage(91)
	did := e.svc.PolicyTick(context.Background())
	var sent []string
	for _, c := range e.runner.callsTo("tmux") {
		if c[0] == "send-keys" && c[len(c)-1] != "Enter" {
			sent = append(sent, c[len(c)-1])
		}
	}
	if len(did) != 1 || strings.Join(sent, "|") != "/model claude-haiku-5-5" {
		t.Fatalf("downgrade did=%v sent=%v", did, sent)
	}
	if again := e.svc.PolicyTick(context.Background()); len(again) != 0 {
		t.Fatalf("switched twice: %v", again)
	}
	usage(40)
	did = e.svc.PolicyTick(context.Background())
	sent = nil
	for _, c := range e.runner.callsTo("tmux") {
		if c[0] == "send-keys" && c[len(c)-1] != "Enter" {
			sent = append(sent, c[len(c)-1])
		}
	}
	if len(did) != 1 || sent[len(sent)-1] != "/model claude-opus-5-5" {
		t.Fatalf("restore did=%v sent=%v", did, sent)
	}
	prev, err := setCodexModel(cfg, "gpt-6-luna")
	b, _ := os.ReadFile(cfg)
	if err != nil || prev != "gpt-6.1-sol" || !strings.HasPrefix(string(b), "model = \"gpt-6-luna\"\n") || !strings.Contains(string(b), "model = \"keep-me\"") {
		t.Fatalf("codex config prev=%q err=%v\n%s", prev, err, b)
	}
	if _, err := e.svc.SetPolicy(true, 80, "haiku; rm -rf /", "gpt-6-luna"); err == nil {
		t.Fatal("bad model accepted")
	}
}

// TestModelFromSlashCommand: a /model switch shows before the next reply.
func TestModelFromSlashCommand(t *testing.T) {
	e := newEnv(t)
	fakeProc(t, e.proc, "4545", "999")
	writeFile(t, filepath.Join(e.home, ".claude", "sessions", "4545.json"), `{"pid":4545,"sessionId":"s-mod","cwd":"/root","procStart":"999","status":"idle"}`)
	writeFile(t, filepath.Join(e.home, ".claude", "projects", "-root", "s-mod.jsonl"),
		`{"type":"assistant","timestamp":"2026-11-04T09:00:00Z","message":{"id":"m1","model":"claude-opus-5-5","content":[{"type":"text","text":"hi"}]}}`+"\n"+
			`{"type":"user","timestamp":"2026-11-04T09:01:00Z","message":{"role":"user","content":"<local-command-stdout>Set model to `+"`Haiku 5.5`"+` and saved as your default for new sessions</local-command-stdout>"}}`+"\n")
	for _, s := range e.col.Snapshot(context.Background()).Sessions {
		if s.SessionID == "s-mod" && s.Model != "Haiku 5.5" {
			t.Fatalf("model = %q", s.Model)
		}
	}
}

// TestIdleIdeas: the propose-only brief goes to the target workspace agent
// only when it is idle long enough and the plan is under the limit; once
// per cooldown.
func TestIdleIdeas(t *testing.T) {
	e := newEnv(t)
	e.now = time.Now()
	fakeProc(t, e.proc, "900", "1")
	fakeProcPPID(t, e.proc, "4242", "777", "900")
	writeFile(t, filepath.Join(e.home, ".claude", "sessions", "4242.json"), `{"pid":4242,"sessionId":"in-tmux-0000","cwd":"/home/dev/docs","procStart":"777","status":"idle"}`)
	tr := filepath.Join(e.home, ".claude", "projects", "-home-dev-docs", "in-tmux-0000.jsonl")
	writeFile(t, tr, `{"type":"assistant","timestamp":"2026-11-04T03:00:01Z","message":{"id":"m","content":[{"type":"text","text":"done"}]}}`+"\n")
	old := time.Now().Add(-2 * time.Hour)
	_ = os.Chtimes(tr, old, old)
	e.runner.sessions = "docs\t1\n"
	e.runner.panes = "docs\tagent\t1\tclaude\t900\n"
	usage := func(p float64) {
		_ = RunStatusline(strings.NewReader(fmt.Sprintf(`{"rate_limits":{"seven_day":{"used_percentage":%v}}}`, p)), io.Discard, e.home, time.Now())
		e.col.invalidate()
	}
	if _, err := e.svc.SetIdlePolicy(true, 30, 50, "docs"); err != nil {
		t.Fatal(err)
	}
	sent := func() int {
		n := 0
		for _, c := range e.runner.agcmdCalls() {
			if strings.Contains(strings.Join(c, " "), "อัตโนมัติจาก AI Agent World") {
				n++
			}
		}
		return n
	}
	usage(70)
	e.svc.PolicyTick(context.Background())
	if sent() != 0 {
		t.Fatal("sent while over the quota limit")
	}
	usage(20)
	e.svc.PolicyTick(context.Background())
	e.svc.PolicyTick(context.Background())
	if sent() != 1 {
		t.Fatalf("sent %d times, want 1 (cooldown)", sent())
	}
	if _, err := e.svc.SetIdlePolicy(true, 30, 50, "web; rm"); err == nil {
		t.Fatal("bad workspace accepted")
	}
}

// TestMessagesCacheFollowsFile: cached answers are reused only while the
// transcript is unchanged; an appended turn shows on the next call.
func TestMessagesCacheFollowsFile(t *testing.T) {
	e := newEnv(t)
	fakeProc(t, e.proc, "4646", "1001")
	writeFile(t, filepath.Join(e.home, ".claude", "sessions", "4646.json"), `{"pid":4646,"sessionId":"s-cache","cwd":"/root","procStart":"1001","status":"idle"}`)
	tr := filepath.Join(e.home, ".claude", "projects", "-root", "s-cache.jsonl")
	line := func(id, text string) string {
		return `{"type":"assistant","timestamp":"2026-11-04T03:00:01Z","message":{"id":"` + id + `","role":"assistant","content":[{"type":"text","text":"` + text + `"}]}}` + "\n"
	}
	writeFile(t, tr, line("m1", "first"))
	m, _ := e.col.Messages(context.Background(), "s-cache", 3)
	m[0].Text = "mutated by caller"
	again, _ := e.col.Messages(context.Background(), "s-cache", 3)
	if len(again) != 1 || again[0].Text != "first" {
		t.Fatalf("cache leaked a caller's change: %+v", again)
	}
	writeFile(t, tr, line("m1", "first")+line("m2", "second"))
	e.col.invalidate()
	fresh, _ := e.col.Messages(context.Background(), "s-cache", 3)
	if len(fresh) != 2 || fresh[1].Text != "second" {
		t.Fatalf("stale after append: %+v", fresh)
	}
}

// TestDaemonListCached: `claude agents` runs once per TTL, again after a write.
func TestDaemonListCached(t *testing.T) {
	e := newEnv(t)
	e.now = time.Now()
	e.runner.agentsJSON = "[]"
	n := func() int {
		k := 0
		for _, c := range e.runner.callsTo("claude") {
			if c[0] == "agents" {
				k++
			}
		}
		return k
	}
	e.col.daemonList(context.Background())
	e.col.daemonList(context.Background())
	if n() != 1 {
		t.Fatalf("claude agents ran %d times within TTL", n())
	}
	e.col.invalidate()
	e.col.daemonList(context.Background())
	e.now = e.now.Add(daemonTTL + time.Second)
	e.col.daemonList(context.Background())
	if n() != 3 {
		t.Fatalf("claude agents ran %d times, want 3", n())
	}
}

// TestRootServesTheApp covers the root path, added so the service can sit on
// its own hostname (aiagent.example.com) without the operator typing /aiagent.
// Root must serve the same page, and — like every other route — must answer
// 404 while the switch is off rather than revealing the service is installed.
func TestRootServesTheApp(t *testing.T) {
	e := newEnv(t)

	code, _, _ := e.do(t, "GET", "/", "", "")
	if code != 404 {
		t.Fatalf("/ while disabled = %d, want 404: anything else announces the service is here", code)
	}

	if _, err := e.store.Enable("test"); err != nil {
		t.Fatal(err)
	}
	code, _, root := e.do(t, "GET", "/", "", "")
	if code != 200 {
		t.Fatalf("/ while enabled = %d, want 200", code)
	}
	_, _, prefixed := e.do(t, "GET", "/aiagent", "", "")
	if root != prefixed {
		t.Error("/ and /aiagent served different bodies; both must be the same application")
	}
	// The page must keep absolute asset URLs, or loading it from / would ask
	// for /app.js, which is not a route.
	if !strings.Contains(root, "/aiagent/app.js") {
		t.Error("page does not reference /aiagent/app.js; its assets would 404 when served from /")
	}
}

// TestRenameSession covers the operator-chosen name. `claude agents` has no
// rename, so this is a local alias: it must change what the snapshot calls the
// session, keep the CLI's own name available to put back, and survive being
// cleared.
func TestRenameSession(t *testing.T) {
	e := newEnv(t)
	tok, _ := e.store.Enable("test")
	fakeProc(t, e.proc, "4242", "777")
	writeFile(t, filepath.Join(e.home, ".claude", "sessions", "4242.json"),
		`{"pid":4242,"sessionId":"s-named","cwd":"/home/dev/web","startedAt":1793750000000,"procStart":"777","name":"first prompt text","status":"idle"}`)

	nameOf := func() (string, string) {
		t.Helper()
		snap := e.svc.applyNames(e.col.Snapshot(context.Background()).Sessions)
		for _, s := range snap {
			if s.SessionID == "s-named" {
				return s.DisplayName, s.OriginalName
			}
		}
		t.Fatal("session s-named not in the snapshot")
		return "", ""
	}

	if got, orig := nameOf(); got != "first prompt text" || orig != "" {
		t.Fatalf("before renaming: %q / %q", got, orig)
	}

	code, m, _ := e.do(t, "POST", "/aiagent/api/sessions/s-named/name", tok, `{"name":"  ตรวจ repo   LittleDays  "}`)
	if code != 200 {
		t.Fatalf("rename: %d %v", code, m)
	}
	// Whitespace is collapsed: the name lands in a heading.
	got, orig := nameOf()
	if got != "ตรวจ repo LittleDays" {
		t.Errorf("renamed to %q", got)
	}
	if orig != "first prompt text" {
		t.Errorf("originalName = %q, want the CLI's own name so the UI can offer it back", orig)
	}

	time.Sleep(writeSpacing)
	if code, m, _ := e.do(t, "POST", "/aiagent/api/sessions/s-named/name", tok, `{"name":""}`); code != 200 {
		t.Fatalf("clearing: %d %v", code, m)
	}
	if got, orig := nameOf(); got != "first prompt text" || orig != "" {
		t.Errorf("after clearing: %q / %q — the CLI's name must come back", got, orig)
	}

	time.Sleep(writeSpacing)
	long := strings.Repeat("ก", maxNameRunes+1)
	if code, _, _ := e.do(t, "POST", "/aiagent/api/sessions/s-named/name", tok, `{"name":"`+long+`"}`); code == 200 {
		t.Error("an over-long name was accepted")
	}

	time.Sleep(writeSpacing)
	if code, _, _ := e.do(t, "POST", "/aiagent/api/sessions/s-named/name", tok, `{"name":"two\nlines"}`); code != 200 {
		t.Error("a newline should be folded to a space, not refused")
	}
	if got, _ := nameOf(); got != "two lines" {
		t.Errorf("newline not folded: %q", got)
	}
}

// TestSnapshotNeverSendsNullArrays guards a failure with no symptoms.
//
// A nil Go slice marshals to `null`. The browser reads `st.snap.runs.length`
// in the list view, which throws on null, and poll() caught that along with
// network errors and moved on — so on a machine with no agcmd runs the list
// view drew nothing, reported nothing, and logged nothing. The map view never
// touched runs.length, which is why it went unnoticed.
//
// Every array the UI iterates has to arrive as an array.
func TestSnapshotNeverSendsNullArrays(t *testing.T) {
	var snap Snapshot // every slice nil, as on a machine with nothing running
	snap.nonNil()

	b, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, k := range []string{"sessions", "runs", "workspaces", "quotas", "warnings"} {
		v, ok := got[k]
		if !ok {
			t.Errorf("snapshot has no %q field at all", k)
			continue
		}
		if v == nil {
			t.Errorf("snapshot.%s is null; the UI calls .length on it and the list view "+
				"renders nothing, silently. It must marshal as []", k)
		}
	}
}

// TestCliRenameBeatsAStaleWebAlias pins which of the two renames wins.
//
// The web alias used to override the CLI name forever, so after `/rename` in
// the CLI the web kept showing the old alias and there was nothing on screen
// to say why. An alias now records the CLI name it replaced and steps aside
// when that changes, because the later rename is the more recent intent.
func TestCliRenameBeatsAStaleWebAlias(t *testing.T) {
	dir := t.TempDir()
	svc := &Service{Store: &Store{dir: dir}, Collector: &Collector{}}

	const id = "s1"
	if _, err := svc.SetSessionName(id, "my name", "CLI original"); err != nil {
		t.Fatalf("set: %v", err)
	}

	// While the CLI name is unchanged, the alias is what shows.
	got := svc.applyNames([]Session{{SessionID: id, DisplayName: "CLI original"}})
	if got[0].DisplayName != "my name" {
		t.Errorf("alias should apply while the CLI name is unchanged; got %q", got[0].DisplayName)
	}
	if got[0].OriginalName != "CLI original" {
		t.Errorf("the CLI name should be kept so the UI can offer it back; got %q", got[0].OriginalName)
	}

	// The CLI is renamed. That is newer, so it wins.
	got = svc.applyNames([]Session{{SessionID: id, DisplayName: "renamed in the CLI"}})
	if got[0].DisplayName != "renamed in the CLI" {
		t.Errorf("a CLI rename must take the name back; got %q — the web alias swallowed it, "+
			"which is the bug this test exists for", got[0].DisplayName)
	}

	// An entry written by the old code has no "over" and cannot be compared,
	// so it keeps overriding rather than being thrown away on a guess.
	if err := os.WriteFile(filepath.Join(dir, namesFile), []byte(`{"s2":"legacy alias"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	got = svc.applyNames([]Session{{SessionID: "s2", DisplayName: "whatever the CLI says"}})
	if got[0].DisplayName != "legacy alias" {
		t.Errorf("a legacy alias has nothing to compare against and must still apply; got %q", got[0].DisplayName)
	}
}
