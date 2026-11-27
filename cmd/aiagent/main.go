// Command aiagent serves AI Agent World and owns its switch.
//
//	aiagent serve [--listen 127.0.0.1:8890] [--state-dir DIR] [--homes A:B]
//	              [--workspaces FILE] [--tls-cert FILE --tls-key FILE]
//	aiagent enable | disable | rotate-token | status
//	aiagent statusline                       (for Claude Code settings.json)
//	aiagent review --title "..." page.html   (an agent hands work back)
//	aiagent capture                          (a room's agent: its page at 390 and 1440 px)
//	aiagent trust-roots list | add DIR | remove DIR | ensure DIR | forget DIR
//
// Safe by default: the listener is loopback-only, and while the switch is off
// every route answers 404 — the service is indistinguishable from absent.
// enable and rotate-token print a new token exactly once; only its SHA-256 is
// stored.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/gofiber/fiber/v3"

	"github.com/hotkhwan/aiagent/internal/audit"
	"github.com/hotkhwan/aiagent/internal/planner"
	"github.com/hotkhwan/aiagent/internal/release"
	"github.com/hotkhwan/aiagent/internal/taskstore"
	"github.com/hotkhwan/aiagent/internal/world"
)

const usage = `aiagent — a village view of the AI coding agents running on this machine

  aiagent serve [flags]     run the service (loopback only unless TLS is configured)
  aiagent enable            turn it on and print a new token (once)
  aiagent rotate-token      replace the token, printing the new one (once)
  aiagent disable           turn it off; the old token stops working
  aiagent status            say whether it is on, without revealing the token
  aiagent statusline        Claude Code status line command; also records plan usage
  aiagent review --title "..." [--summary "..."] [--session ID] [--repo DIR] page.html
                            file an HTML page for the operator to review
  aiagent capture           (in a room) screenshots of the room's preview page
                            at 390 and 1440 px, saved in ~/work/.aiagent/captures
  aiagent trust-roots list | add DIR | remove DIR
                            folders under which Claude Code's folder-trust
                            question is answered Yes before a session starts
  aiagent trust-roots ensure DIR | forget DIR
                            mark one folder trusted (only under a root) / undo
  aiagent verify-release DIR
                            check a downloaded release (SHA256SUMS, its .sig and
                            this machine's archive) against the release key
                            compiled into this aiagent

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
	case "git-credential":
		gitCredential(os.Args[2:])
	case "capture-box":
		captureBox(os.Args[2:])
	case "review":
		review(os.Args[2:])
	case "capture":
		capture(os.Args[2:])
	case "enable", "rotate-token", "disable", "status":
		switchCmd(os.Args[1], os.Args[2:])
	case "trust-roots":
		trustRootsCmd(os.Args[2:])
	case "self-update":
		selfUpdateCmd(os.Args[2:])
	case "verify-release":
		verifyReleaseCmd(os.Args[2:])
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
	workerRepo := fs.String("worker-repo", "", "git checkout that headless workers branch from; empty leaves headless dispatch off")
	workerTest := fs.String("worker-test", "go test ./...", "command that verifies a worker's commit in --worker-repo")
	updateRepo := fs.String("update-repo", "", "git checkout the web page updates from (default: the one packaging/install.sh recorded)")
	pushRelay := fs.String("push-relay", world.DefaultPushRelay, "relay for phone notifications (an https origin), or off")
	proxies := fs.String("trusted-proxies", os.Getenv("AIAGENT_TRUSTED_PROXIES"), "reverse proxies in front of aiagent (CIDRs, comma-separated) whose X-Forwarded-For names the visitor; the Cloudflare tunnel on loopback needs none")
	osUsers := fs.Bool("os-users", false, "give each user token its own OS user and instance (Linux, systemd, root)")
	dutiesConf := fs.String("room-duties", "/etc/aiagent/room-duties.conf", "each room's duty (frontend, backend, tester), with --os-users; written when the admin sets one")
	previewConf := fs.String("preview-conf", "/etc/aiagent/preview.conf", "rooms' preview pages, with --os-users; a missing file or empty HOST leaves them off")
	_ = fs.Parse(args)

	trusted, err := world.ParseTrustedProxies(*proxies)
	if err != nil {
		log.Fatalf("--trusted-proxies: %v", err)
	}
	world.SetTrustedProxies(trusted)

	if (*certFile == "") != (*keyFile == "") {
		log.Fatal("--tls-cert and --tls-key must be given together")
	}
	tls := *certFile != ""
	if !tls && *listen != "systemd" && !isLoopback(*listen) {
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
		AppName: "aiagent",
		// fiber's default is 4 MB; an attached zip may be larger
		BodyLimit:    world.MaxUploadBody,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 60 * time.Second,
	})
	svc := world.NewService(store, col, auditor)
	// The PM's task log. Failing to open it turns the task routes off rather
	// than stopping the service the operator already relies on.
	if ts, err := taskstore.Open(filepath.Join(dir, "tasks.jsonl")); err != nil {
		log.Printf("task log unavailable, task routes are off: %v", err)
	} else {
		if n, err := ts.Recover(); err != nil {
			log.Printf("task log recovery: %v", err)
		} else if n > 0 {
			log.Printf("task log: %d attempt(s) were mid-dispatch at the last stop; marked unknown, not resent", n)
		}
		svc.Tasks = ts
		// An attempt that shows no transcript activity for this long is
		// reported as expired; nothing is requeued by itself.
		svc.Orch = world.NewOrchestrator(ts, col, 45*time.Minute)
		if *workerRepo != "" {
			home, err := os.UserHomeDir()
			if err != nil {
				log.Fatalf("worker: home directory: %v", err)
			}
			svc.Orch.Headless = &world.Headless{Repo: *workerRepo, TestCommand: strings.Fields(*workerTest), StateDir: dir, Home: home}
		}
		// The central PM: a headless claude call on the subscription login,
		// with no tools. It only proposes; without its log its routes stay off.
		if th, err := planner.OpenThread(filepath.Join(dir, "pm.jsonl")); err != nil {
			log.Printf("PM log unavailable, PM routes are off: %v", err)
		} else {
			svc.PM, svc.PMThread = &planner.Cloud{Exec: planner.OSExec{}, Path: col.ClaudeBin()}, th
		}
	}
	if *osUsers {
		svc.OSUsers = &world.OSUsers{SocketDir: "/run/aiagent-users", TokenDir: filepath.Join(dir, "instances"), ModelDir: "/run/aiagent-room", Runner: world.ExecRunner{}}
		svc.Duties = &world.RoomDuties{Path: *dutiesConf}
		startPreview(svc, *previewConf)
	}
	relay, err := world.ParsePushRelay(*pushRelay)
	if err != nil {
		log.Fatalf("push relay: %v", err)
	}
	svc.PushRelay = relay
	svc.UpdateRepo = *updateRepo
	svc.Mount(app)
	svc.StartRooms(context.Background())
	// a room's own instance collects what its sandboxed agent leaves in its
	// work folder (reviews, capture requests)
	svc.StartSpool(context.Background())

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
	if *listen == "systemd" {
		// socket activation: a root-owned unix socket, handed over as fd 3
		ln, err := net.FileListener(os.NewFile(3, "systemd"))
		if err != nil {
			log.Fatalf("--listen systemd needs a socket unit: %v", err)
		}
		if err := app.Listener(ln, cfg); err != nil {
			log.Fatalf("listen: %v", err)
		}
		return
	}
	if err := app.Listen(*listen, cfg); err != nil {
		log.Fatalf("listen: %v", err)
	}
}

// startPreview serves the rooms' pages on their own loopback listener. A bad
// configuration leaves them off and the service running.
func startPreview(svc *world.Service, conf string) {
	cfg, err := world.LoadPreviewConfig(conf)
	if err != nil {
		log.Printf("preview is off: %s: %v", conf, err)
		return
	}
	if cfg == nil {
		return
	}
	p := world.NewPreview(svc, *cfg)
	svc.Preview = p
	if cfg.Host == "" {
		return
	}
	capture := p.EnableCapture(world.CaptureDir, world.ExecRunner{})
	log.Printf("preview on %s (capture: %v)", cfg.Listen, capture)
	go func() { log.Printf("preview stopped: %v", p.Listen()) }()
}

// captureBox runs inside packaging/capture-box.sh.
func captureBox(args []string) {
	if len(args) != 4 {
		log.Fatal("usage: aiagent capture-box CHROMIUM SOCKET PATH OUT (run by packaging/capture-box.sh)")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Second)
	defer cancel()
	if err := world.RunCaptureBox(ctx, args[0], args[1], args[2], args[3]); err != nil {
		log.Fatalf("capture: %v", err)
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
	repo := fs.String("repo", "", "the git checkout the page is about; its last commit is shown as a diff (default: the working directory, when it is a checkout)")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, `usage: aiagent review --title "..." [--summary "..."] [--session ID] [--repo DIR] page.html`)
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
	code := reviewCode(*repo, cwd)
	id := ""
	r, err := world.FileReview(home, *title, *summary, sid, runtime, cwd, code, page, time.Now())
	if err != nil && spoolable(err) {
		// Inside Claude Code's command sandbox ~/.claude is read-only. The
		// room's own instance files the page from the work folder instead.
		id, err = spoolReview(home, world.SpoolRequest{Title: *title, Summary: *summary, SessionID: sid, Runtime: runtime, Cwd: cwd, Code: code, Page: page})
	}
	if err != nil {
		log.Fatalf("review: %v", err)
	}
	if r != nil {
		id = r.ID
	}
	fmt.Printf("filed review %s — open AI Agent World → ห้องทดสอบ to check it\n", id)
	if sid == "" {
		fmt.Println("note: no session id, so the reply can only be copied (pass --session to let the web send it back)")
	}
}

// reviewCode names the commit a review page is about. An explicit --repo that
// is not a usable checkout is an error; the working directory is only a guess.
func reviewCode(repo, cwd string) *world.ReviewCode {
	dir := repo
	if dir == "" {
		dir = cwd
	} else if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	code, err := world.DetectReviewCode(world.RepoGit{R: world.ExecRunner{}}, dir)
	if err != nil {
		if repo != "" {
			log.Fatalf("review: --repo %s: %v", repo, err)
		}
		fmt.Println("note: the page is not tied to a commit (pass --repo <checkout> so the reviewer sees the diff)")
		return nil
	}
	fmt.Printf("code: %s %s..%s (%s)\n", code.Repo, short(code.Base), short(code.Commit), code.BaseRef)
	return code
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// spoolable: the review folder cannot be written here, but the work folder
// may be.
func spoolable(err error) bool {
	return errors.Is(err, syscall.EROFS) || errors.Is(err, syscall.EACCES) || errors.Is(err, syscall.EPERM)
}

func spoolReview(home string, req world.SpoolRequest) (string, error) {
	id, err := world.SpoolReview(home, req)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	r, err := world.SpoolWait(ctx, home, id)
	if err != nil {
		fmt.Fprintf(os.Stderr, "review: left in %s/work/.aiagent/in as %s, but this room's service has not picked it up yet.\n"+
			"It is filed when the service runs; do not file it again. Tell the user if it does not show in the Review view.\n", home, id)
		os.Exit(3)
	}
	if r.Error != "" {
		return "", errors.New(r.Error)
	}
	return r.ReviewID, nil
}

// capture asks the room's own service for screenshots of the room's preview
// page. The admin service makes them in its capture box.
func capture(args []string) {
	fs := flag.NewFlagSet("aiagent capture", flag.ExitOnError)
	wait := fs.Duration("wait", 110*time.Second, "how long to wait for the pictures")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: aiagent capture [--wait 110s]\n\nScreenshots of this room's preview page at 390 and 1440 px, saved in ~/work/.aiagent/captures.")
		fs.PrintDefaults()
	}
	_ = fs.Parse(args)
	if fs.NArg() != 0 {
		fs.Usage()
		os.Exit(2)
	}
	home, _ := os.UserHomeDir()
	id, err := world.SpoolCapture(home)
	if err != nil {
		log.Fatalf("capture: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), *wait)
	defer cancel()
	r, err := world.SpoolWait(ctx, home, id)
	if err != nil {
		fmt.Fprintf(os.Stderr, "capture: no answer yet (request %s). Captures are made only in a room whose admin set up the preview;\n"+
			"if one comes, the pictures appear in %s/work/.aiagent/captures.\n", id, home)
		os.Exit(3)
	}
	if r.Error != "" {
		log.Fatalf("capture: %s", r.Error)
	}
	for _, f := range r.Files {
		fmt.Println(f)
	}
}

// selfUpdateCmd is started by the web page's update button, in a session of
// its own: it pulls, installs and restarts the service that started it.
func selfUpdateCmd(args []string) {
	fs := flag.NewFlagSet("self-update", flag.ExitOnError)
	repo := fs.String("repo", "", "the git checkout to update from")
	to := fs.String("to", "", "the exact upstream commit to install (refuses if upstream moved)")
	relDir := fs.String("release-dir", "", "a downloaded release to verify and install instead of a checkout")
	unit := fs.String("unit", "", "the systemd unit to restart (aiagent@<user>.service)")
	dir := stateDirFlag(fs)
	_ = fs.Parse(args)
	stateDir := *dir
	if stateDir == "" {
		stateDir = world.DefaultStateDir()
	}
	var err error
	if *relDir != "" {
		err = world.RunReleaseUpdate(*relDir, stateDir, *unit)
	} else {
		err = world.RunSelfUpdate(*repo, stateDir, *to)
	}
	if err != nil {
		log.Fatalf("self-update: %v", err)
	}
}

// verifyReleaseCmd checks a downloaded release folder and says which version it
// is; it installs nothing.
func verifyReleaseCmd(args []string) {
	if len(args) != 1 {
		log.Fatal("usage: aiagent verify-release DIR")
	}
	v, err := release.Verify(args[0], runtime.GOOS, runtime.GOARCH, world.ReleasePublicKey())
	if err != nil {
		log.Fatalf("verify-release: %v", err)
	}
	fmt.Printf("ok: aiagent %s for %s/%s is signed by the aiagent release key\n", v.Version, runtime.GOOS, runtime.GOARCH)
}
