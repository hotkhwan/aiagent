// internal/world/paths.go
//
// Where this service keeps its own files. The code this grew out of
// hard-coded absolute /var/lib and /var/log paths because it always ran as
// root on a centrally managed box. This service
// is meant to run as the same unprivileged user as the agents it watches, so
// the default follows the XDG base directory spec and only falls back to a
// system path when there is genuinely no home to use.
//
// Every path is overridable by environment variable or flag, so a packager
// never has to patch the source.
package world

import (
	"os"
	"path/filepath"
)

// appDirName is the single directory name this service owns under a state or
// data root.
const appDirName = "aiagent"

// DefaultStateDir resolves the directory holding the on/off switch, the token
// hash, the policy and uploads, in order of precedence:
//
//	$AIAGENT_STATE_DIR          explicit, wins over everything
//	$XDG_STATE_HOME/aiagent     the spec's answer for per-user state
//	$HOME/.local/state/aiagent  the spec's default when XDG_STATE_HOME is unset
//	/var/lib/aiagent            no home at all (a system service)
//
// The directory is not created here; the Store creates it 0700 on first write.
func DefaultStateDir() string {
	if v := os.Getenv("AIAGENT_STATE_DIR"); v != "" {
		return v
	}
	if v := os.Getenv("XDG_STATE_HOME"); v != "" {
		return filepath.Join(v, appDirName)
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".local", "state", appDirName)
	}
	return filepath.Join("/var/lib", appDirName)
}
