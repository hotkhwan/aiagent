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
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Account is one CLI's sign-in state.
type Account struct {
	Provider string     `json:"provider"` // claude | codex | agy
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
	loginSessions = map[string]string{"claude": "aiagent-login-claude", "codex": "aiagent-login-codex", "agy": "aiagent-login-agy"}
	// agentBox opens an agent CLI the way its own window does (packaging/agent-box.sh).
	agentBox = "/usr/local/lib/aiagent/agent-box.sh"
)

func (c *Collector) loginArgv(provider, email string) (string, []string, error) {
	switch provider {
	case "claude":
		args := []string{"auth", "login", "--claudeai"}
		if email != "" {
			if !regexp.MustCompile(`^[^\s@]{1,64}@[^\s@]{1,190}$`).MatchString(email) {
				return "", nil, &APIError{400, "AIAGENT_BAD_EMAIL", "อีเมลไม่ถูกต้อง"}
			}
			args = append(args, "--email", email)
		}
		return c.ClaudeBin(), args, nil
	case "codex":
		return c.codexBin(), []string{"login", "--device-auth"}, nil
	case "agy":
		if c.agySignedIn() {
			return "", nil, ErrConflict("Antigravity login อยู่ ให้ logout ก่อนจึงใช้บัญชีอื่นได้")
		}
		return agentBox, []string{"agy", "login"}, nil
	}
	return "", nil, &APIError{400, "AIAGENT_BAD_PROVIDER", "provider ต้องเป็น claude, codex หรือ agy"}
}

// Accounts returns the sign-in state of both CLIs plus any login in progress.
func (c *Collector) Accounts(ctx context.Context) []Account {
	out := []Account{c.claudeAccount(ctx), c.codexAccount(ctx), c.agyAccount()}
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
	out, err := c.run(ctx, c.ClaudeBin(), "auth", "status")
	var st struct {
		LoggedIn       bool   `json:"loggedIn"`
		AuthMethod     string `json:"authMethod"`
		Email          string `json:"email"`
		OrgName        string `json:"orgName"`
		LoginExpiresAt string `json:"loginExpiresAt"`
	}
	if i := strings.Index(out, "{"); i >= 0 && json.NewDecoder(strings.NewReader(out[i:])).Decode(&st) == nil {
		a.LoggedIn, a.Method, a.Email, a.Org = st.LoggedIn, st.AuthMethod, st.Email, st.OrgName
		if t := parseTS(st.LoginExpiresAt); !t.IsZero() {
			a.Expires = &t
		}
		return a
	}
	a.Detail = statusDetail(err)
	return a
}

// statusDetail says why a CLI's sign-in state could not be read. What the CLI
// printed is not shown: it is a warning or a stack trace, not a state.
func statusDetail(err error) string {
	if errors.Is(err, exec.ErrNotFound) || errors.Is(err, os.ErrNotExist) {
		return "ยังไม่ได้ติดตั้ง CLI นี้บนเครื่อง"
	}
	return "อ่านสถานะจาก CLI ไม่ได้"
}

func (c *Collector) codexAccount(ctx context.Context) Account {
	a := Account{Provider: "codex"}
	out, err := c.run(ctx, c.codexBin(), "login", "status")
	// the state is one line; Codex prints warnings (a read-only home) around it
	known := false
	for _, l := range strings.Split(out, "\n") {
		l = strings.TrimSpace(l)
		switch low := strings.ToLower(l); {
		case strings.HasPrefix(low, "not logged in"):
			known = true
		case strings.HasPrefix(low, "logged in"):
			known, a.LoggedIn = true, true
			a.Method = truncateRunes(Redact(strings.TrimPrefix(l, "Logged in using ")), 200)
		}
	}
	if !known {
		a.Detail = statusDetail(err)
	}
	return a
}

// agyTokenPath is where Antigravity keeps its sign-in on a machine with no
// keyring. Only its existence is read.
func (c *Collector) agyTokenPath() string {
	return filepath.Join(c.serviceHome(), ".gemini", "antigravity-cli", "antigravity-oauth-token")
}

func (c *Collector) agySignedIn() bool {
	fi, err := os.Stat(c.agyTokenPath())
	return err == nil && fi.Size() > 0
}

func (c *Collector) agyAccount() Account {
	a := Account{Provider: "agy", LoggedIn: c.agySignedIn()}
	if a.LoggedIn {
		a.Method = "Google"
	}
	return a
}

// agyPane drives Antigravity's sign-in screen: it picks Google sign-in, and
// closes the pane once the sign-in is saved. done means it was closed.
func (c *Collector) agyPane(ctx context.Context, name, screen string) (done bool) {
	if c.agySignedIn() {
		_, _ = c.tmux(ctx, "kill-session", "-t", "="+name)
		c.restartAgy(ctx)
		return true
	}
	if strings.Contains(screen, "> 1. Google OAuth") {
		_, _ = c.tmux(ctx, "send-keys", "-t", "="+name+":", "Enter")
	}
	return false
}

// restartAgy reopens Antigravity windows so they read a changed sign-in.
func (c *Collector) restartAgy(ctx context.Context) {
	for _, w := range c.Workspaces {
		if session, window, ok := strings.Cut(w.session, ":"); ok && w.provider == "gemini" {
			_, _ = c.tmux(ctx, "respawn-window", "-k", "-t", "="+session+":="+window)
		}
	}
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
	if provider == "agy" && f.Running && c.agyPane(ctx, name, screen) {
		return nil
	}
	if u := loginURLRE.FindAllString(screen, -1); len(u) > 0 {
		f.URL = strings.TrimRight(u[len(u)-1], ".,)")
	}
	if provider == "codex" {
		f.Code = deviceCodeRE.FindString(screen)
	} else {
		f.NeedsCode = f.Running && (strings.Contains(screen, "Paste code") || provider == "agy" && strings.Contains(screen, "authorization code"))
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
	width := "220"
	if provider == "agy" {
		width = "2000" // it breaks its link at the edge with real line ends
	}
	argv := append([]string{"new-session", "-d", "-s", name, "-x", width, "-y", "50", bin}, args...)
	if out, err := c.tmux(ctx, argv...); err != nil {
		// tmux's own words say why (often: no server, and the service's sandbox
		// may not create one; see packaging/aiagent-tmux@.service)
		return nil, &APIError{502, "AIAGENT_LOGIN_FAILED", "เริ่ม login ไม่สำเร็จ: " + err.Error() + " " + truncateRunes(Redact(strings.TrimSpace(out)), 200)}
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

// SubmitLoginCode types the code a sign-in page showed.
func (c *Collector) SubmitLoginCode(ctx context.Context, provider, code string) error {
	if provider != "claude" && provider != "agy" {
		return &APIError{400, "AIAGENT_BAD_PROVIDER", "provider ต้องเป็น claude หรือ agy"}
	}
	code = strings.TrimSpace(code)
	if !pastedCodeRE.MatchString(code) {
		return &APIError{400, "AIAGENT_BAD_CODE", "รูปแบบไม่ใช่โค้ด login"}
	}
	f := c.loginFlow(ctx, provider)
	if f == nil || !f.Running || (provider == "agy" && !f.NeedsCode) {
		return ErrConflict("ไม่มี login ที่รอโค้ดอยู่ ให้เริ่ม login ก่อน")
	}
	name := loginSessions[provider]
	if _, err := c.tmux(ctx, "send-keys", "-t", "="+name+":", "-l", "--", code); err != nil {
		return &APIError{502, "AIAGENT_LOGIN_FAILED", "พิมพ์เข้าเทอร์มินัลไม่สำเร็จ"}
	}
	c.sleep(300 * time.Millisecond)
	_, err := c.tmux(ctx, "send-keys", "-t", "="+name+":", "Enter")
	return err
}

// CancelLogin closes an in-progress sign-in.
func (c *Collector) CancelLogin(ctx context.Context, provider string) error {
	name, ok := loginSessions[provider]
	if !ok {
		return &APIError{400, "AIAGENT_BAD_PROVIDER", "provider ต้องเป็น claude, codex หรือ agy"}
	}
	_, _ = c.tmux(ctx, "kill-session", "-t", "="+name)
	return nil
}

// Logout signs the CLI out (every session of that CLI on the box).
func (c *Collector) Logout(ctx context.Context, provider string) error {
	var err error
	switch provider {
	case "claude":
		_, err = c.run(ctx, c.ClaudeBin(), "auth", "logout")
	case "codex":
		_, err = c.run(ctx, c.codexBin(), "logout")
	case "agy":
		// It has no logout command outside its own screen. The service sees the
		// home read-only, so tmux, which runs outside that, removes the file.
		_, err = c.tmux(ctx, "run-shell", "rm -f '"+strings.ReplaceAll(c.agyTokenPath(), "'", "")+"'")
		if err == nil {
			c.restartAgy(ctx)
		}
	default:
		return &APIError{400, "AIAGENT_BAD_PROVIDER", "provider ต้องเป็น claude, codex หรือ agy"}
	}
	if err != nil {
		return &APIError{502, "AIAGENT_LOGOUT_FAILED", "logout ไม่สำเร็จ: " + err.Error()}
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
