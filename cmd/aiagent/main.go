// Command aiagent serves AI Agent World and owns its switch.
//
//	aiagent serve [--listen 127.0.0.1:8890] [--state-dir DIR] [--homes A:B]
//	              [--workspaces FILE] [--tls-cert FILE --tls-key FILE]
//	aiagent enable | disable | rotate-token | status
//	aiagent statusline                       (for Claude Code settings.json)
//	aiagent review --title "..." page.html   (an agent hands work back)
//
// Safe by default: the listener is loopback-only, and while the switch is off
// every route answers 404 — the service is indistinguishable from absent.
// enable and rotate-token print a new token exactly once; only its SHA-256 is
// stored.
package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"

	"github.com/hotkhwan/aiagent/internal/audit"
	"github.com/hotkhwan/aiagent/internal/world"
)

const usage = `aiagent — a village view of the AI coding agents running on this machine

  aiagent serve [flags]     run the service (loopback only unless TLS is configured)
  aiagent enable            turn it on and print a new token (once)
  aiagent rotate-token      replace the token, printing the new one (once)
  aiagent disable           turn it off; the old token stops working
  aiagent status            say whether it is on, without revealing the token
  aiagent statusline        Claude Code status line command; also records plan usage
  aiagent review --title "..." [--summary "..."] [--session ID] page.html
                            file an HTML page for the operator to review

Run "aiagent serve --help" for the serve flags.
`

func main() {
	log.SetPrefix("aiagent: ")
	log.SetFlags(0)
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	switch os.Args[1] {
	case "serve":
		serve(os.Args[2:])
	case "statusline":
		// Runs as the user on every status-line refresh. Print a short line and
		// keep only the plan usage numbers. Errors stay silent: a status line
		// that writes an error on every keystroke is worse than a quiet one.
		home, _ := os.UserHomeDir()
		_ = world.RunStatusline(os.Stdin, os.Stdout, home, time.Now())
	case "review":
		review(os.Args[2:])
	case "enable", "rotate-token", "disable", "status":
		switchCmd(os.Args[1], os.Args[2:])
	case "-h", "--help", "help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", os.Args[1], usage)
		os.Exit(2)
	}
}

// stateDirFlag adds --state-dir with the resolved default in its help text, so
// `--help` tells the operator where the files actually are on this machine.
func stateDirFlag(fs *flag.FlagSet) *string {
	return fs.String("state-dir", "", "where the switch, token hash, policy and uploads live (default "+world.DefaultStateDir()+")")
}

func serve(args []string) {
	fs := flag.NewFlagSet("aiagent serve", flag.ExitOnError)
	listen := fs.String("listen", "127.0.0.1:8890", "address to listen on; a non-loopback address requires TLS")
	stateDir := stateDirFlag(fs)
	homes := fs.String("homes", "", "colon-separated home directories to watch (default: this user's home)")
	wsFile := fs.String("workspaces", "", "JSON file listing dispatchable workspaces (default: <state-dir>/workspaces.json)")
	certFile := fs.String("tls-cert", "", "TLS certificate (PEM)")
	keyFile := fs.String("tls-key", "", "TLS private key (PEM)")
	_ = fs.Parse(args)

	if (*certFile == "") != (*keyFile == "") {
		log.Fatal("--tls-cert and --tls-key must be given together")
	}
	tls := *certFile != ""
	if !tls && !isLoopback(*listen) {
		// Deliberately no self-signed fallback. A certificate the operator must
		// click past teaches them to click past certificate warnings, and this
		// service can type into their agents. Terminate TLS in front of it
		// (Tailscale Serve, a reverse proxy) or pass a real certificate.
		log.Fatalf("refusing to listen on %s without TLS: pass --tls-cert/--tls-key, "+
			"or keep --listen on loopback and put Tailscale Serve or a reverse proxy in front", *listen)
	}

	dir := *stateDir
	if dir == "" {
		dir = world.DefaultStateDir()
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		log.Fatalf("state dir: %v", err)
	}
	store := world.NewStore(dir)

	auditor, err := audit.Open(filepath.Join(dir, "audit.jsonl"))
	if err != nil {
		log.Fatalf("audit log: %v", err)
	}
	defer auditor.Close()

	col := world.NewCollector()
	if *homes != "" {
		col.Homes = splitList(*homes)
	} else if home, err := os.UserHomeDir(); err == nil && home != "" {
		col.Homes = []string{home}
	}
	wsPath := *wsFile
	if wsPath == "" {
		wsPath = filepath.Join(dir, "workspaces.json")
	}
	ws, err := world.LoadWorkspaces(wsPath)
	if err != nil {
		log.Fatalf("workspaces: %v", err)
	}
	col.Workspaces = ws

	app := fiber.New(fiber.Config{
		AppName:      "aiagent",
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 60 * time.Second,
	})
	world.NewService(store, col, auditor).Mount(app)

	scheme := "http"
	if tls {
		scheme = "https"
	}
	if store.Enabled() {
		log.Printf("listening on %s://%s/aiagent (%d workspace(s), state %s)", scheme, *listen, len(ws), dir)
	} else {
		log.Printf("listening on %s://%s — switch is OFF, every route answers 404; turn it on with: aiagent enable", scheme, *listen)
	}
	cfg := fiber.ListenConfig{DisableStartupMessage: true}
	if tls {
		cfg.CertFile, cfg.CertKeyFile = *certFile, *keyFile
	}
	if err := app.Listen(*listen, cfg); err != nil {
		log.Fatalf("listen: %v", err)
	}
}

// isLoopback reports whether an address binds only to this machine. A bare
// port or an empty host means "all interfaces", which is not loopback.
func isLoopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil || host == "" {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func splitList(v string) []string {
	var out []string
	for _, p := range strings.Split(v, ":") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// switchCmd turns the service on or off. It needs no privilege beyond write
// access to the state directory: this service runs as the same user as the
// agents it watches, so there is nothing to escalate to.
func switchCmd(sub string, args []string) {
	fs := flag.NewFlagSet("aiagent "+sub, flag.ExitOnError)
	stateDir := stateDirFlag(fs)
	_ = fs.Parse(args)

	dir := *stateDir
	if dir == "" {
		dir = world.DefaultStateDir()
	}
	store := world.NewStore(dir)

	if sub == "status" {
		st, err := store.Load()
		if err != nil {
			log.Fatalf("read switch: %v", err)
		}
		if st.Enabled && st.TokenHash != "" {
			fmt.Printf("AI Agent World: ENABLED\nchanged %s by %s\nstate %s\n",
				st.ChangedAt.Local().Format("2006-01-02 15:04"), st.ChangedBy, dir)
		} else {
			fmt.Printf("AI Agent World: disabled   (turn on with: aiagent enable)\nstate %s\n", dir)
		}
		return
	}

	if err := os.MkdirAll(dir, 0o700); err != nil {
		log.Fatalf("state dir: %v", err)
	}
	auditor, err := audit.Open(filepath.Join(dir, "audit.jsonl"))
	if err != nil {
		log.Fatalf("audit log: %v", err)
	}
	defer auditor.Close()

	actor := "cli"
	if u, err := user.Current(); err == nil {
		actor = "cli:" + u.Username
	}
	if v := os.Getenv("SUDO_USER"); v != "" {
		actor = "cli:" + v
	}

	rpc := "aiagent." + sub
	// Intent first, then the result: a crash between them still leaves a record
	// that someone asked.
	if err := auditor.Write(audit.Record{Event: "intent", RPC: rpc, AdminSub: actor}); err != nil {
		log.Fatalf("audit: %v", err)
	}
	var token string
	switch sub {
	case "enable", "rotate-token":
		token, err = store.Enable(actor)
	case "disable":
		err = store.Disable(actor)
	}
	res := audit.Record{Event: "result", RPC: rpc, AdminSub: actor, Result: "ok"}
	if err != nil {
		res.Result, res.Err = "error", err.Error()
	}
	_ = auditor.Write(res)
	if err != nil {
		log.Fatalf("%s: %v", sub, err)
	}

	if sub == "disable" {
		fmt.Println("AI Agent World: disabled — every route answers 404 and the old token is void.")
		return
	}
	fmt.Printf(`AI Agent World: ENABLED

  token  %s

That token is shown ONCE; only its SHA-256 is stored. Keep it in a password
manager. Start the service with:

  aiagent serve

Replace the token with "aiagent rotate-token"; turn the service off with
"aiagent disable".
`, token)
}

// review files an HTML page for the operator to check in the Testing room.
// It runs as the agent's own user, in the agent's own working directory.
func review(args []string) {
	fs := flag.NewFlagSet("aiagent review", flag.ExitOnError)
	title := fs.String("title", "", "what was done or changed (required)")
	summary := fs.String("summary", "", "short summary shown in the list")
	session := fs.String("session", "", "session id (default: $CLAUDE_CODE_SESSION_ID or $CODEX_SESSION_ID)")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, `usage: aiagent review --title "..." [--summary "..."] [--session ID] page.html`)
		fs.PrintDefaults()
	}
	_ = fs.Parse(args)
	if fs.NArg() != 1 {
		fs.Usage()
		os.Exit(2)
	}
	page, err := os.ReadFile(fs.Arg(0))
	if err != nil {
		log.Fatalf("read page: %v", err)
	}
	sid, runtime := *session, ""
	if os.Getenv("CLAUDECODE") == "1" {
		runtime = "claudeCode"
	} else if os.Getenv("CODEX_SESSION_ID") != "" || os.Getenv("CODEX_THREAD_ID") != "" || os.Getenv("CODEX_SANDBOX") != "" {
		runtime = "codexCli"
	}
	for _, k := range []string{"CLAUDE_CODE_SESSION_ID", "CODEX_SESSION_ID", "CODEX_THREAD_ID"} {
		if sid == "" {
			sid = os.Getenv(k)
		}
	}
	home, _ := os.UserHomeDir()
	cwd, _ := os.Getwd()
	r, err := world.FileReview(home, *title, *summary, sid, runtime, cwd, page, time.Now())
	if err != nil {
		log.Fatalf("review: %v", err)
	}
	fmt.Printf("filed review %s — open AI Agent World → ห้องทดสอบ to check it\n", r.ID)
	if sid == "" {
		fmt.Println("note: no session id, so the reply can only be copied (pass --session to let the web send it back)")
	}
}
