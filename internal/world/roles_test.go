package world

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestUserTokenLifecycleAndLimits(t *testing.T) {
	e := newEnv(t)
	admin, err := e.store.Enable("test")
	if err != nil {
		t.Fatal(err)
	}
	_, user, err := e.store.AddUser("สมชาย")
	if err != nil {
		t.Fatal(err)
	}
	if id, ok := e.store.Identify(user); !ok || id.Role != RoleUser || id.Name != "สมชาย" {
		t.Fatalf("identify = %+v %v", id, ok)
	}
	if e.store.VerifyToken(user) {
		t.Fatal("a user token passed as admin")
	}
	b, _ := os.ReadFile(e.store.path())
	if strings.Contains(string(b), user) {
		t.Fatal("token stored in plain text")
	}
	for _, p := range []struct{ m, path string }{
		{"GET", "/tokens"}, {"POST", "/tokens"}, {"POST", "/tokens/x/revoke"}, {"GET", "/accounts"}, {"GET", "/Accounts"}, {"GET", "/accounts/"},
		{"POST", "/accounts/claude/login"}, {"POST", "/clis/update"}, {"POST", "/policy"}, {"POST", "/codex/model"}, {"GET", "/TOKENS"},
		{"POST", "/sessions/abc/resume"}, {"POST", "/sessions/abc/name"}, {"POST", "/pm/reset"}, {"POST", "/tasks/T-0001/delete"}, {"GET", "/sessions/%2561bc"},
	} {
		body := ""
		if p.m == "POST" {
			body = `{"name":"x"}`
		}
		if code, _, _ := e.do(t, p.m, apiPrefix+p.path, user, body); code != 403 {
			t.Errorf("user %s %s = %d, want 403", p.m, p.path, code)
		}
	}
	if code, _, _ := e.do(t, "GET", apiPrefix+"/snapshot", user, ""); code != 200 {
		t.Errorf("user snapshot = %d", code)
	}
	if code, _, _ := e.do(t, "GET", apiPrefix+"/tokens", admin, ""); code != 200 {
		t.Errorf("admin tokens = %d", code)
	}
	// rotating the admin token keeps users; revoking ends one at once
	if _, err := e.store.Enable("test"); err != nil {
		t.Fatal(err)
	}
	if _, ok := e.store.Identify(user); !ok {
		t.Fatal("rotation dropped the user")
	}
	if _, ok := e.store.Identify(admin); ok {
		t.Fatal("old admin token still works")
	}
	st, _ := e.store.Load()
	if err := e.store.RevokeUser(st.Users[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := e.store.Identify(user); ok {
		t.Fatal("revoked token still works")
	}
}

func TestProtectedSessionIsOnlyListedToUsers(t *testing.T) {
	e := newEnv(t)
	admin, err := e.store.Enable("test")
	if err != nil {
		t.Fatal(err)
	}
	_, user, err := e.store.AddUser("a")
	if err != nil {
		t.Fatal(err)
	}
	const id = "5f2e5f0a-010c-4088-94db-321e7bb0c222"
	if err := os.WriteFile(filepath.Join(e.store.dir, protectedFile), []byte(`{"sessions":["`+id+`"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{id, strings.ToUpper(id), id[:8], "%35" + id[1:], id + "%20"} {
		for _, r := range []struct{ m, tail string }{{"GET", ""}, {"GET", "/messages"}, {"GET", "/suggest"}, {"POST", "/message"}, {"POST", "/model"}, {"POST", "/stop"}} {
			body := ""
			if r.m == "POST" {
				body = `{"message":"hi"}`
			}
			if code, _, _ := e.do(t, r.m, apiPrefix+"/sessions/"+p+r.tail, user, body); code != 403 {
				t.Errorf("user %s %s%s = %d, want 403", r.m, p, r.tail, code)
			}
		}
	}
	if code, _, _ := e.do(t, "GET", apiPrefix+"/sessions/"+id+"/messages", admin, ""); code == 403 {
		t.Error("admin was refused")
	}

	svc := &Service{Store: e.store, Collector: e.col}
	now := time.Now()
	snap := Snapshot{
		Sessions: []Session{{SessionID: id, DisplayName: "SECRET name", Title: "SECRET", Cwd: "/SECRET", LastTool: "SECRET", Model: "SECRET", Doing: &JobDoing{Detail: "SECRET"}, Live: true, StartedAt: &now}, {SessionID: "other", Title: "open"}},
		Pending:  []Pending{{SessionID: id, Message: "SECRET"}},
	}
	b, _ := json.Marshal(svc.forRole(Identity{Role: RoleUser}, snap))
	if strings.Contains(string(b), "SECRET") {
		t.Fatalf("leaked to a user: %s", b)
	}
	if !strings.Contains(string(b), id) || !strings.Contains(string(b), "open") {
		t.Fatalf("a session went missing: %s", b)
	}
	b, _ = json.Marshal(svc.forRole(Identity{Role: RoleAdmin}, snap))
	if !strings.Contains(string(b), "SECRET") {
		t.Fatal("admin lost the text")
	}

	// an unreadable list protects everything
	if err := os.WriteFile(filepath.Join(e.store.dir, protectedFile), []byte(`{`), 0o600); err != nil {
		t.Fatal(err)
	}
	if !svc.protectionsFor(nil).covers("other") {
		t.Fatal("a corrupt list must fail closed")
	}
}

func TestProtectedWorkspaceTakesNoWork(t *testing.T) {
	e := newEnv(t)
	e.col.Workspaces = []workspaceEntry{{name: "admin", session: "admin", dir: "/x", provider: "claude", protected: true}}
	if _, err := e.col.Mission(context.Background(), "admin", "do it"); err == nil {
		t.Error("a mission reached the admin workspace")
	}
	if _, err := pickWorkspace(e.col.Workspaces, "claude", nil, func(workspaceEntry) bool { return true }); err == nil {
		t.Error("routing picked the admin workspace")
	}
	if _, err := e.store.Enable("test"); err != nil {
		t.Fatal(err)
	}
	_, user, _ := e.store.AddUser("a")
	if code, _, _ := e.do(t, "POST", apiPrefix+"/messages", user, `{"workspace":"admin","message":"hi"}`); code != 403 {
		t.Errorf("user message to the admin workspace = %d", code)
	}
	// a session seen there stays protected after it ends
	svc := &Service{Store: e.store, Collector: e.col}
	svc.protectionsFor([]Session{{SessionID: "abcd1234", DispatchVia: "admin"}})
	if !svc.protectionsFor(nil).covers("abcd1234") {
		t.Error("protection was forgotten")
	}
}
