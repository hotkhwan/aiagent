package world

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
)

type outRunner struct {
	out string
	err error
}

func (o outRunner) Run(context.Context, string, ...string) ([]byte, error) {
	return []byte(o.out), o.err
}

// The accounts dialog showed whatever the CLI printed when it was signed out.
func TestAccountsNeverShowRawCLIText(t *testing.T) {
	failed := errors.New("exit status 1")
	for name, c := range map[string]struct {
		out      string
		err      error
		loggedIn bool
		codex    bool
		detail   bool
	}{
		"claude signed out":           {out: "{\"loggedIn\": false}\n", err: failed},
		"claude with a warning after": {out: "{\"loggedIn\": true, \"email\": \"a@b.c\"}\nWARN raw text", loggedIn: true},
		"claude broken":               {out: "TypeError: raw text\n  at x.js", err: failed, detail: true},
		"claude missing":              {err: &exec.Error{Name: "claude", Err: exec.ErrNotFound}, detail: true},
		"codex signed out":            {codex: true, out: "WARNING: raw text\nNot logged in\n", err: failed},
		"codex signed in":             {codex: true, out: "WARNING: raw text\nLogged in using ChatGPT\nWARNING: raw text", loggedIn: true},
		"codex broken":                {codex: true, out: "Error: raw text", err: failed, detail: true},
	} {
		col := &Collector{Runner: outRunner{c.out, c.err}}
		a := col.claudeAccount(t.Context())
		if c.codex {
			a = col.codexAccount(t.Context())
		}
		if a.LoggedIn != c.loggedIn || (a.Detail != "") != c.detail {
			t.Errorf("%s: %+v", name, a)
		}
		if strings.Contains(a.Detail+a.Method, "raw text") || strings.Contains(a.Detail, "{") {
			t.Errorf("%s: raw CLI text reaches the page: %+v", name, a)
		}
	}
}
