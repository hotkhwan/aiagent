package world

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type unitRunner struct {
	calls []string
	fail  bool
}

func (u *unitRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	u.calls = append(u.calls, name+" "+strings.Join(args, " "))
	if u.fail {
		return []byte("boom"), os.ErrInvalid
	}
	return nil, nil
}

func osUserEnv(t *testing.T) (*env, *unitRunner, *OSUsers) {
	t.Helper()
	e := newEnv(t)
	dir := shortTempDir(t)
	r := &unitRunner{}
	o := &OSUsers{SocketDir: dir, TokenDir: dir, Runner: r}
	e.svc.OSUsers = o
	return e, r, o
}

// shortTempDir is a temporary folder for unix sockets. t.TempDir is too long
// on macOS: under $TMPDIR (/var/folders/...) plus the test's name, a socket
// path passes the 104-byte limit of a unix address and every listen fails.
func shortTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "aa")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// instance stands in for a user's own aiagent on its socket.
func instance(t *testing.T, o *OSUsers, name string, h http.HandlerFunc) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(o.TokenDir, name+".token"), []byte("inst-"+name+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("unix", filepath.Join(o.SocketDir, name+".sock"))
	if err != nil {
		t.Fatal(err)
	}
	// Read the whole body first, as the real instance (fiber) does; a handler
	// that answers without reading it breaks the pipe on macOS, whose unix
	// sockets buffer far less than Linux's.
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(b))
		h(w, r)
	})}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
}

func TestUserRequestsGoOnlyToTheirInstance(t *testing.T) {
	e, _, o := osUserEnv(t)
	admin, err := e.store.Enable("t")
	if err != nil {
		t.Fatal(err)
	}
	_, user, err := e.store.AddUser("vibe-a", "vibe-a")
	if err != nil {
		t.Fatal(err)
	}
	var seen []string
	instance(t, o, "vibe-a", func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Method+" "+r.URL.Path+" "+r.Header.Get(TokenHeader))
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`{"code":"SUCCESS","details":{"sessions":[],"from":"vibe-a"}}`))
	})
	code, _, body := e.do(t, "GET", apiPrefix+"/snapshot", user, "")
	if code != 200 || !strings.Contains(body, `"from":"vibe-a"`) {
		t.Fatalf("snapshot = %d %s", code, body)
	}
	// (read again for the teammate list)
	for _, x := range seen {
		if x != "GET "+apiPrefix+"/snapshot inst-vibe-a" {
			t.Fatalf("instance saw %v", seen)
		}
	}
	if len(seen) == 0 {
		t.Fatal("nothing was forwarded")
	}
	// admin-only routes are refused here, before anything is forwarded; and a
	// user cannot name another instance
	seen = nil
	if code, _, _ := e.do(t, "GET", apiPrefix+"/accounts", user, ""); code != 403 || len(seen) != 0 {
		t.Fatalf("accounts = %d, forwarded %v", code, seen)
	}
	if code, _, body := e.do(t, "GET", apiPrefix+"/snapshot", admin, ""); code != 200 || strings.Contains(body, "vibe-a") {
		t.Fatalf("admin was sent to a user instance: %s", body)
	}
	// a user token without an account reaches nothing
	_, loose, _ := e.store.AddUser("loose", "")
	if code, _, _ := e.do(t, "GET", apiPrefix+"/snapshot", loose, ""); code != 403 {
		t.Fatalf("token without an account = %d", code)
	}
}

func TestTokenCreatesAndRemovesTheOSUser(t *testing.T) {
	e, r, _ := osUserEnv(t)
	admin, err := e.store.Enable("t")
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"Root", "a b", "../x", "-x", "x;y", ""} {
		code, _, _ := e.do(t, "POST", apiPrefix+"/tokens", admin, `{"name":"`+bad+`"}`)
		if code != 400 && code != 429 {
			t.Errorf("name %q = %d", bad, code)
		}
		e.svc.lastWrite = e.svc.lastWrite.Add(-writeSpacing)
	}
	if len(r.calls) != 0 {
		t.Fatalf("a bad name reached systemd: %v", r.calls)
	}
	r.fail = true
	if code, _, _ := e.do(t, "POST", apiPrefix+"/tokens", admin, `{"name":"vibe-a"}`); code != 502 {
		t.Fatalf("failed add = %d", code)
	}
	if st, _ := e.store.Load(); len(st.Users) != 0 {
		t.Fatal("a token was left without its account")
	}
	r.fail, r.calls = false, nil
	e.svc.lastWrite = e.svc.lastWrite.Add(-writeSpacing)
	code, _, body := e.do(t, "POST", apiPrefix+"/tokens", admin, `{"name":"vibe-a"}`)
	if code != 200 || len(r.calls) != 1 || r.calls[0] != "systemctl start aiagent-osuser-add@vibe-a.service" {
		t.Fatalf("add = %d %v", code, r.calls)
	}
	i := strings.Index(body, "kaw_")
	tok := body[i : i+47]
	st, _ := e.store.Load()
	id := st.Users[0].ID
	// a removal that fails still ends access, and can be retried
	r.fail = true
	e.svc.lastWrite = e.svc.lastWrite.Add(-writeSpacing)
	if code, _, _ := e.do(t, "POST", apiPrefix+"/tokens/"+id+"/revoke", admin, `{}`); code != 502 {
		t.Fatalf("failed removal = %d", code)
	}
	if _, ok := e.store.Identify(tok); ok {
		t.Fatal("access outlived a failed removal")
	}
	r.fail, r.calls = false, nil
	e.svc.lastWrite = e.svc.lastWrite.Add(-writeSpacing)
	if code, _, _ := e.do(t, "POST", apiPrefix+"/tokens/"+id+"/revoke", admin, `{}`); code != 200 || r.calls[0] != "systemctl start aiagent-osuser-del@vibe-a.service" {
		t.Fatalf("removal = %d %v", code, r.calls)
	}
	if st, _ := e.store.Load(); len(st.Users) != 0 {
		t.Fatal("the entry was left")
	}
}

func TestEmptyBackgroundIsNotListed(t *testing.T) {
	e := Session{SessionID: "e27d2e78-1bb9", Kind: "bg", DisplayName: "e27d2e78"}
	if !emptyBackground(e) {
		t.Error("an empty background process was listed")
	}
	view := Session{SessionID: "9ded4266-1cb6", Kind: "interactive", DisplayName: "web-23", parked: true}
	if !emptyBackground(view) {
		t.Error("a terminal on the agent view was listed")
	}
	named, used, term := e, e, e
	named.DisplayName = "anna"
	used.sourcePath = "/t.jsonl"
	term.Kind = "interactive"
	for _, s := range []Session{named, used, term} {
		if emptyBackground(s) {
			t.Errorf("hidden: %+v", s)
		}
	}
}

func TestParkedTerminalIsNotListedBesideItsJob(t *testing.T) {
	term := Session{SessionID: "ddf84e75-aaaa", Live: true, parkedJob: "95126f47"}
	job := Session{SessionID: "95126f47-bbbb", Live: true}
	if got := dropParked([]Session{term, job}); len(got) != 1 || got[0].SessionID != job.SessionID {
		t.Fatalf("both listed: %v", got)
	}
	if got := dropParked([]Session{term}); len(got) != 1 {
		t.Fatal("the terminal was dropped with no job to stand for it")
	}
}

func TestUserReadsTeammatesButCannotWrite(t *testing.T) {
	e, _, o := osUserEnv(t)
	if _, err := e.store.Enable("t"); err != nil {
		t.Fatal(err)
	}
	_, a, _ := e.store.AddUser("anna", "anna")
	_, _, _ = e.store.AddUser("bob", "bob")
	if err := os.WriteFile(filepath.Join(e.store.dir, protectedFile), []byte(`{"sessions":["adm-0000-1111"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var bobSaw, annaSaw []string
	serve := func(saw *[]string, sessions string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			*saw = append(*saw, r.Method+" "+strings.TrimPrefix(r.URL.Path, apiPrefix))
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"code":"SUCCESS","details":{"sessions":` + sessions + `}}`))
		}
	}
	instance(t, o, "anna", serve(&annaSaw, `[{"sessionId":"anna-sess","displayName":"anna","live":true,"messageVia":"agcmd"}]`))
	// bob also claims anna's session and the admin's: neither may be taken over
	instance(t, o, "bob", serve(&bobSaw, `[{"sessionId":"bob-sess","displayName":"bob","live":true,"messageVia":"agcmd","stopVia":"x"},{"sessionId":"anna-sess","live":true},{"sessionId":"adm-0000-1111","live":true,"title":"FAKE"}]`))

	code, _, body := e.do(t, "GET", apiPrefix+"/snapshot", a, "")
	if code != 200 || !strings.Contains(body, "bob-sess") || strings.Contains(body, "FAKE") || strings.Count(body, "anna-sess") != 1 {
		t.Fatalf("snapshot = %d %s", code, body)
	}
	i := strings.Index(body, `"sessionId":"bob-sess"`)
	if b := body[i:]; !strings.Contains(b, `"readOnly":true`) || strings.Contains(b[:strings.Index(b, "}")], `"messageVia"`) {
		t.Fatalf("a teammate's session is writable: %s", b)
	}
	bobSaw, annaSaw = nil, nil
	if code, _, _ := e.do(t, "GET", apiPrefix+"/sessions/bob-sess/messages", a, ""); code != 200 || len(bobSaw) == 0 || bobSaw[len(bobSaw)-1] != "GET /sessions/bob-sess/messages" {
		t.Fatalf("reading a teammate = %d, bob saw %v", code, bobSaw)
	}
	for _, r := range []string{"message", "model", "stop", "name"} {
		bobSaw = nil
		if code, _, _ := e.do(t, "POST", apiPrefix+"/sessions/bob-sess/"+r, a, `{"message":"x"}`); code != 403 {
			t.Errorf("POST %s to a teammate = %d", r, code)
		}
		for _, x := range bobSaw {
			if strings.HasPrefix(x, "POST") {
				t.Errorf("a write reached the teammate: %s", x)
			}
		}
	}
	if code, _, _ := e.do(t, "GET", apiPrefix+"/sessions/bob-sess/suggest", a, ""); code != 403 {
		t.Errorf("suggest on a teammate = %d", code)
	}
	if code, _, _ := e.do(t, "GET", apiPrefix+"/sessions/adm-0000-1111/messages", a, ""); code != 403 {
		t.Errorf("the admin's chat = %d", code)
	}
	annaSaw = nil
	if code, _, _ := e.do(t, "POST", apiPrefix+"/sessions/anna-sess/message", a, `{"message":"x"}`); code != 200 || annaSaw[len(annaSaw)-1] != "POST /sessions/anna-sess/message" {
		t.Errorf("own message = %d %v", code, annaSaw)
	}
	for _, p := range []string{"/pm", "/tasks", "/runs/x/log"} {
		if code, _, _ := e.do(t, "GET", apiPrefix+p, a, ""); code != 403 {
			t.Errorf("user GET %s = %d", p, code)
		}
	}
}
