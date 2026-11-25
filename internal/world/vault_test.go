package world

import (
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
	const tok = "glpat-EXAMPLE_0123456789abcdef"
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
	if code, _, _ := e.do(t, "POST", apiPrefix+"/vault", admin, `{"name":"bad name","provider":"gitlab","purpose":"x y z","value":"`+tok+`"}`); code == 200 {
		t.Fatal("a bad name was accepted")
	}
	if code, _, _ := e.do(t, "POST", apiPrefix+"/vault", admin, `{"name":"two","provider":"gitlab","purpose":"two words","value":"has a space in it ok"}`); code == 200 {
		t.Fatal("a token with spaces was accepted")
	}
}
