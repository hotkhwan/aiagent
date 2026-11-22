package main

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/user"
	"path/filepath"

	"github.com/hotkhwan/aiagent/internal/audit"
	"github.com/hotkhwan/aiagent/internal/world"
)

const trustRootsUsage = `usage:
  aiagent trust-roots list                 roots in effect, and where they come from
  aiagent trust-roots add DIR              trust Claude Code folders under DIR
  aiagent trust-roots remove DIR           stop trusting folders under DIR
  aiagent trust-roots ensure DIR           mark DIR trusted now (only if under a root)
  aiagent trust-roots forget DIR           remove DIR's trust entry from ~/.claude.json

Run it as the user the service runs as: the roots live in that user's state
directory and the trust in that user's ~/.claude.json. Every subcommand takes
--state-dir. $` + world.TrustedRootsEnv + ` (":"-separated) overrides the stored list.
`

// trustRootsCmd manages the trusted roots (world/trust.go). It works the same
// on macOS and Linux: everything is files in the state directory and $HOME.
func trustRootsCmd(args []string) {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, trustRootsUsage)
		os.Exit(2)
	}
	sub := args[0]
	fs := flag.NewFlagSet("aiagent trust-roots "+sub, flag.ExitOnError)
	stateDir := stateDirFlag(fs)
	fs.Usage = func() { fmt.Fprint(os.Stderr, trustRootsUsage); fs.PrintDefaults() }
	_ = fs.Parse(args[1:])
	dir := *stateDir
	if dir == "" {
		dir = world.DefaultStateDir()
	}
	needPath := func() string {
		if fs.NArg() != 1 {
			fmt.Fprint(os.Stderr, trustRootsUsage)
			os.Exit(2)
		}
		return fs.Arg(0)
	}

	switch sub {
	case "list":
		stored, err := world.StoredTrustedRoots(dir)
		if err != nil {
			log.Fatalf("read roots: %v", err)
		}
		if v := os.Getenv(world.TrustedRootsEnv); v != "" {
			fmt.Printf("in effect (from $%s, overrides the stored list):\n", world.TrustedRootsEnv)
			for _, r := range world.LoadTrustedRoots(dir) {
				fmt.Println("  " + r)
			}
			fmt.Println("stored in " + filepath.Join(dir, "policy.json") + ":")
		}
		if len(stored) == 0 {
			fmt.Println("  (none: no folder is trusted automatically)")
		}
		for _, r := range stored {
			fmt.Println("  " + r)
		}
	case "add", "remove":
		root, err := world.NormalizeRoot(needPath())
		if err != nil {
			log.Fatalf("%s: %v", sub, err)
		}
		stored, err := world.StoredTrustedRoots(dir)
		if err != nil {
			log.Fatalf("read roots: %v", err)
		}
		var next []string
		found := false
		for _, r := range stored {
			if r == root {
				found = true
				if sub == "remove" {
					continue
				}
			}
			next = append(next, r)
		}
		if sub == "add" && !found {
			if fi, err := os.Stat(root); err != nil || !fi.IsDir() {
				fmt.Fprintf(os.Stderr, "note: %s is not an existing folder yet; adding it anyway\n", root)
			}
			next = append(next, root)
		}
		if sub == "remove" && !found {
			log.Fatalf("%s is not a trusted root (see aiagent trust-roots list)", root)
		}
		withAudit(dir, "aiagent.trustRoots."+sub, root, func() error { return world.SetTrustedRoots(dir, next) })
		fmt.Printf("%s %s\n", map[string]string{"add": "trusted root added:", "remove": "trusted root removed:"}[sub], root)
		if os.Getenv(world.TrustedRootsEnv) != "" {
			fmt.Printf("note: $%s is set and overrides the stored list\n", world.TrustedRootsEnv)
		}
	case "ensure":
		folder, err := filepath.Abs(needPath())
		if err != nil {
			log.Fatal(err)
		}
		home := homeDir()
		var changed []string
		withAudit(dir, "aiagent.autoTrust", folder, func() error {
			var err error
			changed, err = world.EnsureFolderTrusted(home, folder, world.LoadTrustedRoots(dir))
			return err
		})
		if len(changed) == 0 {
			fmt.Println("already trusted: " + folder)
		}
		for _, p := range changed {
			fmt.Println("trusted: " + p)
		}
	case "forget":
		folder, err := filepath.Abs(needPath())
		if err != nil {
			log.Fatal(err)
		}
		var changed bool
		withAudit(dir, "aiagent.trustRoots.forget", folder, func() error {
			var err error
			changed, err = world.ForgetFolderTrust(homeDir(), folder)
			return err
		})
		if changed {
			fmt.Println("trust entry removed: " + folder)
		} else {
			fmt.Println("no trust entry for " + folder)
		}
	default:
		fmt.Fprintf(os.Stderr, "unknown trust-roots command %q\n\n%s", sub, trustRootsUsage)
		os.Exit(2)
	}
}

func homeDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		log.Fatal("no home directory: run as the service user")
	}
	return home
}

// withAudit runs fn between an intent and a result line in the audit log,
// the way switchCmd does; the folder goes in as a hash only.
func withAudit(stateDir, rpc, path string, fn func() error) {
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		log.Fatalf("state dir: %v", err)
	}
	auditor, err := audit.Open(filepath.Join(stateDir, "audit.jsonl"))
	if err != nil {
		log.Fatalf("audit log: %v", err)
	}
	defer auditor.Close()
	actor := "cli"
	if u, err := user.Current(); err == nil {
		actor = "cli:" + u.Username
	}
	ph := world.ParamsHash(path)
	if err := auditor.Write(audit.Record{Event: "intent", RPC: rpc, AdminSub: actor, ParamsHash: ph}); err != nil {
		log.Fatalf("audit: %v", err)
	}
	err = fn()
	res := audit.Record{Event: "result", RPC: rpc, AdminSub: actor, ParamsHash: ph, Result: "ok"}
	if err != nil {
		res.Result, res.Err = "error", err.Error()
	}
	_ = auditor.Write(res)
	if errors.Is(err, world.ErrNotUnderRoot) {
		log.Fatalf("%s is not under a trusted root; add one with: aiagent trust-roots add <folder>", path)
	}
	if err != nil {
		log.Fatalf("%v", err)
	}
}
