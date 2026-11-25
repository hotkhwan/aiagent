package world

import (
	"context"
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
	dir := t.TempDir()
	r := &unitRunner{}
	o := &OSUsers{SocketDir: dir, TokenDir: dir, Runner: r}
	e.svc.OSUsers = o
	return e, r, o
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
	srv := &http.Server{Handler: h}
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
	if len(seen) != 1 || seen[0] != "GET "+apiPrefix+"/snapshot inst-vibe-a" {
		t.Fatalf("instance saw %v", seen)
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

func TestUnclaimedSpareIsNotListed(t *testing.T) {
	pt := procTable{7: {argv: []string{"claude", "bg-spare", "--bg-spare", "/x"}}}
	if !pt.unclaimedSpare(Session{Pid: 7, Kind: "bg"}) {
		t.Error("a spare was listed")
	}
	if pt.unclaimedSpare(Session{Pid: 7, Kind: "bg", sourcePath: "/t.jsonl"}) {
		t.Error("a claimed session was hidden")
	}
}
