package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hotkhwan/aiagent/internal/world"
)

func TestGitCredentialAnswersOnlyItsOwnHost(t *testing.T) {
	dir := t.TempDir()
	svc := &world.Service{Store: world.NewStore(dir)}
	if _, err := svc.AddVault("gl", "gitlab", "push code", "EXAMPLE_0123456789abcdef"); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), "aiagent")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("%s", out)
	}
	ask := func(op, in string) string {
		c := exec.Command(bin, "git-credential", "--state-dir", dir, "--name", "gl", op)
		c.Stdin = strings.NewReader(in)
		c.Env = os.Environ()
		out, _ := c.Output()
		return string(out)
	}
	if got := ask("get", "protocol=https\nhost=gitlab.com\n\n"); !strings.Contains(got, "password=EXAMPLE_0123456789abcdef") {
		t.Fatalf("own host: %q", got)
	}
	for _, in := range []string{"protocol=https\nhost=evil.example\n\n", "protocol=http\nhost=gitlab.com\n\n", "protocol=https\nhost=github.com\n\n", ""} {
		if got := ask("get", in); got != "" {
			t.Errorf("%q answered %q", in, got)
		}
	}
	if got := ask("store", "protocol=https\nhost=gitlab.com\n\n"); got != "" {
		t.Errorf("store answered %q", got)
	}
}
