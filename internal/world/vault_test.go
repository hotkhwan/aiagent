package world

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVaultKeepsValuesOutOfTheListAndOutOfReach(t *testing.T) {
	e := newEnv(t)
	admin, err := e.store.Enable("t")
	if err != nil {
		t.Fatal(err)
	}
	_, user, _ := e.store.AddUser("anna", "")
	const tok = "glpat-EXAMPLE_0123456789abcdef" // gitleaks:allow — a made-up EXAMPLE token: the test checks a user cannot store one
	if code, _, _ := e.do(t, "POST", apiPrefix+"/vault", user, `{"name":"x","provider":"gitlab","purpose":"push","value":"`+tok+`"}`); code != 403 {
		t.Fatalf("a user saved a token: %d", code)
	}
	if code, _, _ := e.do(t, "GET", apiPrefix+"/vault", user, ""); code != 403 {
		t.Fatalf("a user listed the vault: %d", code)
	}
	code, _, body := e.do(t, "POST", apiPrefix+"/vault", admin, `{"name":"gitlab-netra-ui","provider":"gitlab","purpose":"push netra-ui and open MRs","value":"`+tok+`"}`)
	if code != 200 {
		t.Fatalf("save = %d %s", code, body)
	}
	if _, _, body := e.do(t, "GET", apiPrefix+"/vault", admin, ""); strings.Contains(body, tok) || !strings.Contains(body, "gitlab-netra-ui") {
		t.Fatalf("list shows the value or misses the name: %s", body)
	}
	if v, err := e.svc.VaultValue("gitlab-netra-ui"); err != nil || v != tok {
		t.Fatalf("value = %q %v", v, err)
	}
	fi, err := os.Stat(filepath.Join(e.store.dir, vaultSecrets))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("secrets file mode = %v %v", fi.Mode(), err)
	}
	if code, _, _ := e.do(t, "POST", apiPrefix+"/vault", admin, `{"name":"bad\\nname","provider":"gitlab","purpose":"x y z","value":"`+tok+`"}`); code == 200 {
		t.Fatal("a bad name was accepted")
	}
	if code, _, _ := e.do(t, "POST", apiPrefix+"/vault", admin, `{"name":"two","provider":"gitlab","purpose":"two words","value":"has a space in it ok"}`); code == 200 {
		t.Fatal("a token with spaces was accepted")
	}
}

// While the admin looks at a room, its requests go to that room's instance; a
// token saved then must not.
func TestVaultNeverReachesARoom(t *testing.T) {
	e, _, o := osUserEnv(t)
	admin, err := e.store.Enable("t")
	if err != nil {
		t.Fatal(err)
	}
	_, _, _ = e.store.AddUser("vibe-a", "vibe-a")
	var seen []string
	instance(t, o, "vibe-a", func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Method+" "+r.URL.Path)
		_, _ = w.Write([]byte(`{"code":"SUCCESS","details":{}}`))
	})
	req := httptest.NewRequest("POST", apiPrefix+"/vault", strings.NewReader(`{"name":"gl","provider":"gitlab","purpose":"push code","value":"EXAMPLE_0123456789abcdef"}`))
	req.Header.Set(TokenHeader, admin)
	req.Header.Set(AsHeader, "vibe-a")
	req.Header.Set("Content-Type", "application/json")
	resp, err := e.app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	if len(seen) != 0 {
		t.Fatalf("the token was sent to the room: %v", seen)
	}
	if v, err := e.svc.VaultValue("gl"); resp.StatusCode != 200 || err != nil || v == "" {
		t.Fatalf("not saved here: %d %v", resp.StatusCode, err)
	}
}

func TestVaultEditKeepsOrReplacesTheValue(t *testing.T) {
	e := newEnv(t)
	a, err := e.svc.AddVault("GitLab Netra โปรเจกต์", "gitlab", "push code", "EXAMPLE_0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.AddVault("gitlab netra โปรเจกต์", "gitlab", "push code", "EXAMPLE_0123456789abcdef"); err == nil {
		t.Fatal("the same name in another case was accepted")
	}
	if _, err := e.svc.EditVault(a.ID, "renamed", "github", "open MRs", ""); err != nil {
		t.Fatal(err)
	}
	if v, _ := e.svc.VaultValue("renamed"); v != "EXAMPLE_0123456789abcdef" {
		t.Fatalf("an empty value changed the token: %q", v)
	}
	if _, err := e.svc.EditVault(a.ID, "renamed", "github", "open MRs", "EXAMPLE_second_0123456789"); err != nil {
		t.Fatal(err)
	}
	if v, _ := e.svc.VaultValue("renamed"); v != "EXAMPLE_second_0123456789" {
		t.Fatalf("not replaced: %q", v)
	}
	if err := e.svc.RemoveVault(a.ID); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(e.store.dir, vaultSecrets)); strings.Contains(string(b), "EXAMPLE") {
		t.Fatal("the value outlived its entry")
	}
}
