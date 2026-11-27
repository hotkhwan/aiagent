package main

import (
	"context"
	"flag"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/hotkhwan/aiagent/internal/world"
)

// channelCmd is the MCP server Claude Code spawns as a channel
// (world/channelmcp.go). stdout carries MCP only; notes go to stderr, which
// Claude Code keeps in its debug log. No credential is taken on the command
// line: the key is read from the state directory, the session id from Claude
// Code's own pid registry.
func channelCmd(args []string) {
	fs := flag.NewFlagSet("aiagent channel", flag.ExitOnError)
	stateDir := stateDirFlag(fs)
	base := fs.String("url", envOr("AIAGENT_URL", "http://127.0.0.1:8890"), "the local aiagent service (loopback only)")
	_ = fs.Parse(args)
	if err := world.CheckChannelURL(*base); err != nil {
		os.Stderr.WriteString("aiagent channel: " + err.Error() + "\n")
		os.Exit(2)
	}
	dir := *stateDir
	if dir == "" {
		dir = world.DefaultStateDir()
	}
	home, _ := os.UserHomeDir()
	ppid := os.Getppid()
	svc := &world.ChannelHTTP{
		BaseURL: *base,
		KeyPath: world.ChannelKeyPath(dir),
		Session: func() (string, error) { return world.ChannelSessionID(home, ppid, os.Getenv) },
		// longer than the service's 25 s poll
		Client: &http.Client{Timeout: 40 * time.Second},
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := world.RunChannel(ctx, os.Stdin, os.Stdout, os.Stderr, svc); err != nil {
		os.Stderr.WriteString("aiagent channel: " + err.Error() + "\n")
		os.Exit(1)
	}
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
