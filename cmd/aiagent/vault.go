package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/hotkhwan/aiagent/internal/world"
)

// vaultHosts is where a provider's token may be sent. git asks the helper for
// whatever host a remote names, so the helper, not the remote, decides.
var vaultHosts = map[string]string{"gitlab": "gitlab.com", "github": "github.com"}

// gitCredential is a git credential helper over the vault:
//
//	git -c credential.helper='!aiagent git-credential --name NAME' push
//
// It answers only "get", over https, for the token's own provider. The value
// goes to git on stdout and nowhere else.
func gitCredential(args []string) {
	fs := flag.NewFlagSet("aiagent git-credential", flag.ExitOnError)
	stateDir := stateDirFlag(fs)
	name := fs.String("name", "", "vault entry to use")
	_ = fs.Parse(args)
	if fs.Arg(0) != "get" || *name == "" {
		return // store and erase are not ours to do
	}
	ask := map[string]string{}
	sc := bufio.NewScanner(os.Stdin)
	for sc.Scan() {
		if k, v, ok := strings.Cut(sc.Text(), "="); ok {
			ask[k] = v
		}
	}
	dir := *stateDir
	if dir == "" {
		dir = world.DefaultStateDir()
	}
	svc := &world.Service{Store: world.NewStore(dir)}
	list, err := svc.Vault()
	if err != nil {
		fmt.Fprintln(os.Stderr, "aiagent: cannot read the vault")
		os.Exit(1)
	}
	for _, e := range list {
		if e.Name != *name {
			continue
		}
		if ask["protocol"] != "https" || vaultHosts[e.Provider] == "" || ask["host"] != vaultHosts[e.Provider] {
			fmt.Fprintln(os.Stderr, "aiagent: this token is not for that host")
			return
		}
		v, err := svc.VaultValue(*name)
		if err != nil || v == "" {
			fmt.Fprintln(os.Stderr, "aiagent: the vault has no value for that name")
			os.Exit(1)
		}
		fmt.Printf("username=oauth2\npassword=%s\n", v)
		return
	}
	fmt.Fprintln(os.Stderr, "aiagent: no vault entry with that name")
}
