// internal/aiagent/accounts.go
//
// Account switching for the CLIs on this box, so the operator can move
// Claude Code or Codex to another account without a terminal. Every step
// runs the vendor CLI's own login flow (argv only, never a shell) in a
// private tmux session; the web shows the sign-in link (and Codex's device
// code) and, for Claude, sends back the pasted code.
//
// Effects worth knowing (shown in the UI):
//   - `claude auth login` keeps the current account until a code is accepted.
//   - `codex login` removes ~/.codex/auth.json as soon as it starts: Codex is
//     logged out until the new sign-in completes.
//   - logout signs out every session of that CLI on the box.
package world

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Account is one CLI's sign-in state.
type Account struct {
	Provider string     `json:"provider"` // claude | codex
	LoggedIn bool       `json:"loggedIn"`
	Method   string     `json:"method,omitempty"`
	Email    string     `json:"email,omitempty"`
	Org      string     `json:"org,omitempty"`
	Expires  *time.Time `json:"expiresAt,omitempty"`
	Detail   string     `json:"detail,omitempty"`
	Login    *LoginFlow `json:"login,omitempty"`
}

// LoginFlow is an in-progress sign-in started from the web.
type LoginFlow struct {
	URL       string `json:"url,omitempty"`
	Code      string `json:"code,omitempty"` // Codex device code to type on the site
	NeedsCode bool   `json:"needsCode"`      // Claude: paste the code shown after sign-in
	Running   bool   `json:"running"`
	Output    string `json:"output,omitempty"` // last lines, redacted
}

var (
	loginURLRE    = regexp.MustCompile(`https://[^\s"'<>]+`)
	deviceCodeRE  = regexp.MustCompile(`\b[A-Z0-9]{4}-[A-Z0-9]{4,5}\b`)
	pastedCodeRE  = regexp.MustCompile(`^[A-Za-z0-9_.~#=+/-]{8,1000}$`) // OAuth code#state (base64url)
	loginSessions = map[string]string{"claude": "aiagent-login-claude", "codex": "aiagent-login-codex"}
)

func (c *Collector) loginArgv(provider, email string) (string, []string, error) {
	switch provider {
	case "claude":
		args := []string{"auth", "login", "--claudeai"}
		if email != "" {
			if !regexp.MustCompile(`^[^\s@]{1,64}@[^\s@]{1,190}$`).MatchString(email) {
				return "", nil, &APIError{400, "AIAGENT_BAD_EMAIL", "invalid email"}
			}
			args = append(args, "--email", email)
		}
		return c.ClaudePath, args, nil
	case "codex":
		return c.CodexPath, []string{"login", "--device-auth"}, nil
	}
	return "", nil, &APIError{400, "AIAGENT_BAD_PROVIDER", "provider must be claude or codex"}
}

// Accounts returns the sign-in state of both CLIs plus any login in progress.
func (c *Collector) Accounts(ctx context.Context) []Account {
	out := []Account{c.claudeAccount(ctx), c.codexAccount(ctx)}
	for i := range out {
		out[i].Login = c.loginFlow(ctx, out[i].Provider)
	}
	return out
}

func (c *Collector) run(ctx context.Context, bin string, args ...string) (string, error) {
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	b, err := c.Runner.Run(cctx, bin, args...)
	return string(b), err
}

func (c *Collector) claudeAccount(ctx context.Context) Account {
	a := Account{Provider: "claude"}
	out, _ := c.run(ctx, c.ClaudePath, "auth", "status")
	var st struct {
		LoggedIn       bool   `json:"loggedIn"`
		AuthMethod     string `json:"authMethod"`
		Email          string `json:"email"`
		OrgName        string `json:"orgName"`
		LoginExpiresAt string `json:"loginExpiresAt"`
	}
	if i := strings.Index(out, "{"); i >= 0 && json.Unmarshal([]byte(out[i:]), &st) == nil {
		a.LoggedIn, a.Method, a.Email, a.Org = st.LoggedIn, st.AuthMethod, st.Email, st.OrgName
		if t := parseTS(st.LoginExpiresAt); !t.IsZero() {
			a.Expires = &t
		}
		return a
	}
	a.Detail = truncateRunes(Redact(strings.TrimSpace(out)), 200)
	return a
}

func (c *Collector) codexAccount(ctx context.Context) Account {
	a := Account{Provider: "codex"}
	out, _ := c.run(ctx, c.CodexPath, "login", "status")
	out = strings.TrimSpace(out)
	low := strings.ToLower(out)
	a.LoggedIn = strings.Contains(low, "logged in") && !strings.Contains(low, "not logged in")
	if a.LoggedIn {
		a.Method = strings.TrimSpace(strings.TrimPrefix(out, "Logged in using "))
	}
	a.Detail = truncateRunes(Redact(out), 200)
	return a
}

func (c *Collector) loginFlow(ctx context.Context, provider string) *LoginFlow {
	name := loginSessions[provider]
	if _, err := c.tmux(ctx, "has-session", "-t", "="+name); err != nil {
		return nil
	}
	screen, _ := c.tmux(ctx, "capture-pane", "-p", "-J", "-t", "="+name+":")
	cmd, _ := c.tmux(ctx, "list-panes", "-t", "="+name, "-F", "#{pane_dead} #{pane_dead_status}")
	dead := strings.Fields(strings.TrimSpace(cmd))
	f := &LoginFlow{Running: len(dead) == 0 || dead[0] != "1"}
	if !f.Running {
		// finished: a clean exit needs no leftovers on screen; a failed one
		// is shown once so the operator can read why
		_, _ = c.tmux(ctx, "kill-session", "-t", "="+name)
		if len(dead) > 1 && dead[1] == "0" {
			return nil
		}
	}
	var keep []string
	for _, l := range strings.Split(screen, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(l), "Pane is dead") {
			keep = append(keep, l)
		}
	}
	screen = strings.Join(keep, "\n")
	if u := loginURLRE.FindAllString(screen, -1); len(u) > 0 {
		f.URL = strings.TrimRight(u[len(u)-1], ".,)")
	}
	if provider == "codex" {
		f.Code = deviceCodeRE.FindString(screen)
	} else {
		f.NeedsCode = f.Running && strings.Contains(screen, "Paste code")
	}
	lines := strings.Split(strings.TrimSpace(screen), "\n")
	if len(lines) > 6 {
		lines = lines[len(lines)-6:]
	}
	tail := strings.Join(lines, "\n")
	if f.URL != "" {
		tail = strings.ReplaceAll(tail, f.URL, "«link above»")
	}
	f.Output = truncateRunes(Redact(tail), 600)
	return f
}

// StartLogin begins the CLI's sign-in in a private tmux session (replacing
// any earlier attempt) and waits briefly for its link.
func (c *Collector) StartLogin(ctx context.Context, provider, email string) (*LoginFlow, error) {
	bin, args, err := c.loginArgv(provider, email)
	if err != nil {
		return nil, err
	}
	name := loginSessions[provider]
	_, _ = c.tmux(ctx, "kill-session", "-t", "="+name)
	argv := append([]string{"new-session", "-d", "-s", name, "-x", "220", "-y", "50", bin}, args...)
	if _, err := c.tmux(ctx, argv...); err != nil {
		return nil, &APIError{502, "AIAGENT_LOGIN_FAILED", "could not start login: " + err.Error()}
	}
	// keep the pane after the CLI exits so its last words can be read
	_, _ = c.tmux(ctx, "set-option", "-w", "-t", "="+name+":", "remain-on-exit", "on")
	for i := 0; i < 30; i++ {
		if f := c.loginFlow(ctx, provider); f != nil && (f.URL != "" || !f.Running) {
			return f, nil
		}
		c.sleep(400 * time.Millisecond)
	}
	return c.loginFlow(ctx, provider), nil
}

// SubmitLoginCode types the code Claude's sign-in page showed.
func (c *Collector) SubmitLoginCode(ctx context.Context, code string) error {
	code = strings.TrimSpace(code)
	if !pastedCodeRE.MatchString(code) {
		return &APIError{400, "AIAGENT_BAD_CODE", "that does not look like a sign-in code"}
	}
	f := c.loginFlow(ctx, "claude")
	if f == nil || !f.Running {
		return ErrConflict("no Claude sign-in is waiting for a code; start one first")
	}
	name := loginSessions["claude"]
	if _, err := c.tmux(ctx, "send-keys", "-t", "="+name+":", "-l", "--", code); err != nil {
		return &APIError{502, "AIAGENT_LOGIN_FAILED", "send failed"}
	}
	c.sleep(300 * time.Millisecond)
	_, err := c.tmux(ctx, "send-keys", "-t", "="+name+":", "Enter")
	return err
}

// CancelLogin closes an in-progress sign-in.
func (c *Collector) CancelLogin(ctx context.Context, provider string) error {
	name, ok := loginSessions[provider]
	if !ok {
		return &APIError{400, "AIAGENT_BAD_PROVIDER", "provider must be claude or codex"}
	}
	_, _ = c.tmux(ctx, "kill-session", "-t", "="+name)
	return nil
}

// Logout signs the CLI out (every session of that CLI on the box).
func (c *Collector) Logout(ctx context.Context, provider string) error {
	var err error
	switch provider {
	case "claude":
		_, err = c.run(ctx, c.ClaudePath, "auth", "logout")
	case "codex":
		_, err = c.run(ctx, c.CodexPath, "logout")
	default:
		return &APIError{400, "AIAGENT_BAD_PROVIDER", "provider must be claude or codex"}
	}
	if err != nil {
		return &APIError{502, "AIAGENT_LOGOUT_FAILED", "logout failed: " + err.Error()}
	}
	return nil
}

// CodexModel is one entry of Codex's own model list.
type CodexModel struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// CodexModels reads the model list Codex caches (~/.codex/models_cache.json).
func (c *Collector) CodexModels() []CodexModel {
	b, err := os.ReadFile(filepath.Join(c.serviceHome(), ".codex", "models_cache.json"))
	if err != nil {
		return []CodexModel{}
	}
	var d struct {
		Models []struct {
			Slug        string `json:"slug"`
			DisplayName string `json:"display_name"`
		} `json:"models"`
	}
	_ = json.Unmarshal(b, &d)
	out := []CodexModel{}
	for _, m := range d.Models {
		if codexModelRE.MatchString(m.Slug) {
			out = append(out, CodexModel{m.Slug, firstNonEmpty(m.DisplayName, m.Slug)})
		}
	}
	return out
}

// CodexDefaultModel is `model` in ~/.codex/config.toml ("" when unset).
func (c *Collector) CodexDefaultModel() string {
	b, err := os.ReadFile(filepath.Join(c.serviceHome(), ".codex", "config.toml"))
	if err != nil {
		return ""
	}
	if m := codexModelKey.FindString(string(b)); m != "" {
		return strings.Trim(strings.TrimSpace(strings.SplitN(m, "=", 2)[1]), `"`)
	}
	return ""
}
