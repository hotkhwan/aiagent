// Package sandbox runs a worker CLI under macOS sandbox-exec: writes only
// where the policy says, and no reads of the operator's secrets.
package sandbox

import (
	"fmt"
	"path/filepath"
	"strings"
)

// Policy is what one worker process may touch.
type Policy struct {
	// Writable are the directories the process may write under — its worktree
	// and its temp dir.
	Writable []string
	// Denied are paths it may neither read nor write: the aiagent state
	// directory, ~/.ssh, ~/.cloudflared and the keychain.
	Denied []string
}

// devNodes are the character devices shells and tools write to; everything
// else under /dev stays read-only.
var devNodes = []string{"/dev/null", "/dev/tty", "/dev/stdout", "/dev/stderr"}

// Wrap returns argv prefixed with sandbox-exec running argv under p.
func Wrap(p Policy, argv ...string) ([]string, error) {
	if len(argv) == 0 {
		return nil, fmt.Errorf("sandbox: empty command")
	}
	profile, err := p.profile()
	if err != nil {
		return nil, err
	}
	return append([]string{"/usr/bin/sandbox-exec", "-p", profile}, argv...), nil
}

// profile is a Scheme profile: allow by default, deny writes, re-allow the
// writable subtrees and the device nodes, then deny reads of the secrets.
// Later rules win in sandbox-exec, which is what makes the re-allows work.
func (p Policy) profile() (string, error) {
	var b strings.Builder
	b.WriteString("(version 1)\n(allow default)\n(deny file-write*)\n")
	for _, d := range devNodes {
		fmt.Fprintf(&b, "(allow file-write* (literal %s))\n", quote(d))
	}
	for _, w := range p.Writable {
		r, err := realPath(w)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&b, "(allow file-write* (subpath %s))\n", quote(r))
	}
	for _, d := range p.Denied {
		r, err := realPath(d)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&b, "(deny file-read* file-write* (subpath %s))\n", quote(r))
	}
	return b.String(), nil
}

// realPath resolves symlinks (macOS temp dirs live under /var, which is
// /private/var) because sandbox-exec matches on the real path. A path that
// does not exist yet is kept as given: a denied secret may be absent.
func realPath(p string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	if r, err := filepath.EvalSymlinks(abs); err == nil {
		return r, nil
	}
	return abs, nil
}

// quote makes a Scheme string literal. Paths containing a quote or a
// backslash are escaped; a newline cannot appear in a path we resolved.
func quote(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return `"` + s + `"`
}

// DefaultPolicy is the policy every attempt runs under: the attempt's worktree
// and temp dir are writable; the operator's home secrets, the aiagent state
// directory and the keychain are denied.
func DefaultPolicy(home, stateDir, worktree, tmp string) Policy {
	return Policy{
		Writable: []string{worktree, tmp},
		Denied: []string{
			filepath.Join(home, ".ssh"),
			filepath.Join(home, ".cloudflared"),
			filepath.Join(home, "Library", "Keychains"),
			filepath.Join(home, "littledays-keys"),
			stateDir,
		},
	}
}
