package world

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// cliSpec is one agent CLI the update button refreshes. npm is the package
// used when the CLI was installed through npm; empty means no npm route.
type cliSpec struct {
	name string
	npm  string
}

var agentCLIs = []cliSpec{
	{name: "claude", npm: "@anthropic-ai/claude-code"},
	{name: "codex", npm: "@openai/codex"},
	{name: "gemini", npm: "@google/gemini-cli"},
	{name: "amp", npm: "@sourcegraph/amp"},
}

// cliPlan is how one installed CLI is updated: native (its own `update`),
// npm (a global install of the latest package) or manual (no known command).
type cliPlan struct {
	Name   string
	Path   string
	Method string
	argv   []string
}

// CLIInfo is one installed CLI as the web lists it.
type CLIInfo struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Method  string `json:"method"`
	Version string `json:"version"`
}

// CLIUpdate is what one CLI did during an update run.
type CLIUpdate struct {
	Name   string `json:"name"`
	Path   string `json:"path"`
	Method string `json:"method"`
	Before string `json:"before"`
	After  string `json:"after"`
	Error  string `json:"error,omitempty"`
	Output string `json:"output,omitempty"`
}

// CLIUpdateStatus is the latest run: whether it is still going and the CLIs
// it has finished so far.
type CLIUpdateStatus struct {
	Running bool        `json:"running"`
	Results []CLIUpdate `json:"results"`
}

// cliState holds the update run and the Claude model menu read from the
// installed binary. menuMu is separate so reading the binary never blocks
// the status poll.
type cliState struct {
	mu      sync.Mutex
	running bool
	results []CLIUpdate

	menuMu  sync.Mutex
	menuKey string
	menu    [][2]string
}

const cliUpdateTimeout = 10 * time.Minute

var (
	versionRE = regexp.MustCompile(`\d+\.\d+\.\d+`)
	// The id must end on a boundary: "claude-opus-4-1-20250805" is a dated
	// alias of 4.1, and the menu only lists the bare ids.
	claudeModelIDRE = regexp.MustCompile(`claude-(opus|sonnet|haiku|fable)-([0-9]+)-([0-9]{1,2})`)
	claudeFamilies  = []string{"fable", "opus", "sonnet", "haiku"}
)

var claudeAliasMenu = [][2]string{
	{"opus", "Opus (ล่าสุด)"}, {"sonnet", "Sonnet (ล่าสุด)"}, {"haiku", "Haiku (ล่าสุด)"},
}

func (c *Collector) cliPath(name string) (string, error) {
	if name == "claude" && c.ClaudePath != "" {
		return c.ClaudePath, nil
	}
	return exec.LookPath(name)
}

func (c *Collector) planCLIUpdates() []cliPlan {
	var plan []cliPlan
	for _, spec := range agentCLIs {
		path, err := c.cliPath(spec.name)
		if err != nil {
			continue
		}
		real, err := filepath.EvalSymlinks(path)
		if err != nil {
			real = path
		}
		p := cliPlan{Name: spec.name, Path: path, Method: "manual"}
		switch {
		case strings.Contains(real, "/claude/versions/"):
			p.Method, p.argv = "native", []string{path, "update"}
		case strings.Contains(real, "/node_modules/") && spec.npm != "":
			p.Method, p.argv = "npm", []string{"npm", "install", "-g", spec.npm + "@latest"}
		}
		plan = append(plan, p)
	}
	return plan
}

func (c *Collector) cliVersion(ctx context.Context, path string) string {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	out, _ := c.Runner.Run(ctx, path, "--version")
	return versionRE.FindString(string(out))
}

// InstalledCLIs lists the known agent CLIs found on this machine's PATH.
func (c *Collector) InstalledCLIs(ctx context.Context) []CLIInfo {
	var out []CLIInfo
	for _, p := range c.planCLIUpdates() {
		out = append(out, CLIInfo{Name: p.Name, Path: p.Path, Method: p.Method, Version: c.cliVersion(ctx, p.Path)})
	}
	return out
}

// StartCLIUpdate runs every installed CLI's updater in the background. A
// run takes minutes, longer than the HTTP write timeout, so the caller polls
// CLIUpdateStatus. The returned channel closes when the run ends.
func (c *Collector) StartCLIUpdate() (<-chan struct{}, error) {
	c.cli.mu.Lock()
	defer c.cli.mu.Unlock()
	if c.cli.running {
		return nil, ErrConflict("a CLI update is already running")
	}
	c.cli.running = true
	c.cli.results = nil
	done := make(chan struct{})
	go func() {
		defer close(done)
		c.runCLIUpdates(context.Background())
		c.cli.mu.Lock()
		c.cli.running = false
		c.cli.mu.Unlock()
	}()
	return done, nil
}

// CLIUpdateStatus is the latest run, safe to read while one is in progress.
func (c *Collector) CLIUpdateStatus() CLIUpdateStatus {
	c.cli.mu.Lock()
	defer c.cli.mu.Unlock()
	return CLIUpdateStatus{Running: c.cli.running, Results: append([]CLIUpdate{}, c.cli.results...)}
}

func (c *Collector) runCLIUpdates(ctx context.Context) {
	for _, p := range c.planCLIUpdates() {
		r := CLIUpdate{Name: p.Name, Path: p.Path, Method: p.Method, Before: c.cliVersion(ctx, p.Path)}
		if p.Method == "manual" {
			r.Error = "no known update command for this CLI; update it by hand"
		} else {
			uctx, cancel := context.WithTimeout(ctx, cliUpdateTimeout)
			out, err := c.Runner.Run(uctx, p.argv[0], p.argv[1:]...)
			cancel()
			r.Output = truncateRunes(string(out), 1200)
			if err != nil {
				r.Error = err.Error()
			}
		}
		r.After = c.cliVersion(ctx, p.Path)
		c.cli.mu.Lock()
		c.cli.results = append(c.cli.results, r)
		c.cli.mu.Unlock()
	}
}

// ClaudeModelMenu is the model choice the web offers for Claude Code. The
// explicit ids come from the installed binary, which is the CLI's own list,
// so an update that adds a model shows up without a code change.
func (c *Collector) ClaudeModelMenu() [][2]string {
	path, err := c.cliPath("claude")
	if err != nil {
		return claudeAliasMenu
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		real = path
	}
	fi, err := os.Stat(real)
	if err != nil {
		return claudeAliasMenu
	}
	key := fmt.Sprintf("%s|%d|%d", real, fi.Size(), fi.ModTime().UnixNano())

	c.cli.menuMu.Lock()
	defer c.cli.menuMu.Unlock()
	if key == c.cli.menuKey {
		return c.cli.menu
	}
	bin, err := os.ReadFile(real)
	if err != nil {
		return claudeAliasMenu
	}
	c.cli.menuKey, c.cli.menu = key, newestClaudeModels(bin)
	return c.cli.menu
}

// newestClaudeModels keeps the highest version of each family found in the
// binary, then appends the aliases, which the CLI itself resolves to the
// latest model.
func newestClaudeModels(bin []byte) [][2]string {
	type candidate struct {
		id           string
		major, minor int
	}
	newest := map[string]candidate{}
	for _, m := range claudeModelIDRE.FindAllSubmatchIndex(bin, -1) {
		if end := m[1]; end < len(bin) && (bin[end] == '-' || (bin[end] >= '0' && bin[end] <= '9')) {
			continue
		}
		family := string(bin[m[2]:m[3]])
		major, _ := strconv.Atoi(string(bin[m[4]:m[5]]))
		minor, _ := strconv.Atoi(string(bin[m[6]:m[7]]))
		if old, ok := newest[family]; ok && (old.major > major || (old.major == major && old.minor >= minor)) {
			continue
		}
		newest[family] = candidate{string(bin[m[0]:m[1]]), major, minor}
	}
	var menu [][2]string
	for _, f := range claudeFamilies {
		c, ok := newest[f]
		if !ok {
			continue
		}
		title := strings.ToUpper(f[:1]) + f[1:]
		menu = append(menu, [2]string{c.id, fmt.Sprintf("%s %d.%d", title, c.major, c.minor)})
	}
	return append(menu, claudeAliasMenu...)
}
