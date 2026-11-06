// internal/world/workspaces.go
//
// A workspace is one named checkout an agent works in: a directory plus the
// tmux session whose agent pane serves it. This table used to be compiled in,
// naming one company's repositories and home directories, and a test kept it
// in lock-step with that company's private dispatch script. Neither can ship,
// so the table is configuration now: the service starts with none and reads
// whatever the operator declares.
//
// The file is JSON rather than the YAML the proposal sketched, to keep the
// module's only external dependency the HTTP framework — everything else this
// service persists (state, policy) is already JSON, and a public repository is
// a poor place to add a parser it does not otherwise need.
//
//	{
//	  "workspaces": [
//	    {"name": "web", "tmuxSession": "web", "dir": "/home/alice/src/web"}
//	  ]
//	}
package world

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// workspaceEntry is one configured workspace: the short name the UI and the
// API use, the tmux session that must be running an agent CLI, and the
// directory a session's cwd is matched against.
type workspaceEntry struct {
	name    string
	session string
	dir     string
}

// workspaceConfig is the on-disk shape. Keys are camelCase, like every other
// JSON this service reads or writes.
type workspaceConfig struct {
	Workspaces []struct {
		Name        string `json:"name"`
		TmuxSession string `json:"tmuxSession"`
		Dir         string `json:"dir"`
	} `json:"workspaces"`
}

// WorkspaceInfo is a configured workspace in a form callers outside this
// package can use — the agcmd CLI reads the same file, so that both it and
// the service refuse exactly the same names.
type WorkspaceInfo struct {
	Name        string
	TmuxSession string
	Dir         string
}

// LoadWorkspaceList reads the config and returns it in exported form. Same
// file, same validation as LoadWorkspaces.
func LoadWorkspaceList(path string) ([]WorkspaceInfo, error) {
	ws, err := LoadWorkspaces(path)
	if err != nil {
		return nil, err
	}
	out := make([]WorkspaceInfo, 0, len(ws))
	for _, w := range ws {
		out = append(out, WorkspaceInfo{Name: w.name, TmuxSession: w.session, Dir: w.dir})
	}
	return out, nil
}

// DefaultWorkspacesPath is where both the service and agcmd look when no
// explicit path is given.
func DefaultWorkspacesPath() string {
	return filepath.Join(DefaultStateDir(), "workspaces.json")
}

// LoadWorkspaces reads a workspace config. A missing file is not an error: it
// means "no workspaces", which is the correct state for a view-only install
// and for the `none` driver. Every entry must carry all three fields and an
// absolute directory, so a typo fails at startup rather than silently
// dispatching a message into the wrong checkout.
func LoadWorkspaces(path string) ([]workspaceEntry, error) {
	if path == "" {
		return nil, nil
	}
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read workspaces: %w", err)
	}
	var cfg workspaceConfig
	if err := json.Unmarshal(b, &cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	seen := map[string]bool{}
	out := make([]workspaceEntry, 0, len(cfg.Workspaces))
	for i, w := range cfg.Workspaces {
		switch {
		case w.Name == "":
			return nil, fmt.Errorf("%s: workspace %d has no name", path, i)
		case w.TmuxSession == "":
			return nil, fmt.Errorf("%s: workspace %q has no tmuxSession", path, w.Name)
		case w.Dir == "":
			return nil, fmt.Errorf("%s: workspace %q has no dir", path, w.Name)
		case !filepath.IsAbs(w.Dir):
			return nil, fmt.Errorf("%s: workspace %q dir %q is not absolute", path, w.Name, w.Dir)
		case seen[w.Name]:
			return nil, fmt.Errorf("%s: workspace %q declared twice", path, w.Name)
		}
		seen[w.Name] = true
		out = append(out, workspaceEntry{
			name:    w.Name,
			session: w.TmuxSession,
			dir:     filepath.Clean(w.Dir),
		})
	}
	return out, nil
}

// lookupWorkspace resolves a name the browser supplied. An unknown name is
// refused by the caller; it is never passed through to a shell.
func (c *Collector) lookupWorkspace(name string) (workspaceEntry, bool) {
	for _, w := range c.Workspaces {
		if w.name == name {
			return w, true
		}
	}
	return workspaceEntry{}, false
}

// workspaceFor names the workspace a session's cwd sits in, preferring the
// longest matching directory so a nested checkout wins over its parent.
func (c *Collector) workspaceFor(cwd string) string {
	best, bestLen := "", 0
	for _, w := range c.Workspaces {
		if (cwd == w.dir || strings.HasPrefix(cwd, w.dir+"/")) && len(w.dir) > bestLen {
			best, bestLen = w.name, len(w.dir)
		}
	}
	return best
}
