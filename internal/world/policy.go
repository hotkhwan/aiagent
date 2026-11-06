// internal/aiagent/policy.go
//
// Auto model downgrade. When the operator turns it on, a background check
// (every minute) compares the provider-reported plan usage with a threshold:
//
//   - Claude: every live Claude Code session the web can reach (agcmd/relay)
//     that is not already on the economy model gets `/model <economy>`; the
//     model it had is remembered and put back once usage falls below the
//     restore level (threshold − 15 points) or the window resets.
//   - Codex: running Codex sessions cannot be switched from outside, so the
//     economy model becomes the default for NEW sessions (`model` in
//     ~/.codex/config.toml of the service home); the previous value is
//     restored the same way.
//
// Never estimated: with no "actual" reading nothing changes. Every switch is
// audited as aiagent.autoModel.
package world

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/hotkhwan/aiagent/internal/audit"
)

// Policy is the operator's auto-downgrade setting plus what it changed.
type Policy struct {
	Enabled      bool              `json:"enabled"`
	ThresholdPct float64           `json:"thresholdPct"`
	ClaudeModel  string            `json:"claudeModel"`
	CodexModel   string            `json:"codexModel"`
	Switched     map[string]string `json:"switched,omitempty"` // sessionId → model it had
	CodexPrev    *string           `json:"codexPrev,omitempty"`
	LastAction   string            `json:"lastAction,omitempty"`
	LastActionAt *time.Time        `json:"lastActionAt,omitempty"`
	ChangedAt    *time.Time        `json:"changedAt,omitempty"`

	// Idle ideas: when the target agent has been idle long enough and both
	// plans have room, ask it (once per cooldown) to PROPOSE improvements to
	// AI Agent World — it may consult Codex read-only — and file the result
	// as a review page. It must not change code until the operator approves.
	IdleIdeas    bool       `json:"idleIdeas"`
	IdleMinutes  int        `json:"idleMinutes"`
	IdleMaxQuota float64    `json:"idleMaxQuota"`
	IdleTarget   string     `json:"idleTarget"` // agcmd workspace name
	LastIdeaAt   *time.Time `json:"lastIdeaAt,omitempty"`
}

const ideaCooldown = 6 * time.Hour

// IdeaBrief is the message typed into the idle agent.
const IdeaBrief = `[อัตโนมัติจาก AI Agent World · agent ว่างเกินเกณฑ์ และ quota ยังเหลือ] ` +
	`ใช้ช่วงว่างนี้ "เสนอ" การปรับปรุง AI Agent World — ห้ามแก้โค้ด ห้าม commit/push/deploy จนกว่าผู้ใช้จะอนุมัติ. ` +
	`ทำ: (1) ไอเดียให้เป็นเกมมากขึ้น สดใส สมจริง ไม่น่าเบื่อ (2) asset ที่ควรหาหรือขอเพิ่ม พร้อมเหตุผล (3) ช่องโหว่หรือจุดที่ช้า/หนักเครื่องที่เจอ. ` +
	`ปรึกษา Codex ได้แบบอ่านอย่างเดียว: codex exec --sandbox read-only "<คำถาม>". ` +
	`สรุปเป็นหน้า HTML สั้น ๆ แล้วส่งด้วย aiagent review --title "ข้อเสนอช่วงว่าง: <หัวข้อ>" page.html แล้วหยุดรอคำตอบในห้องทดสอบ`

const restoreGap = 15

var (
	claudeModelRE = regexp.MustCompile(`^(opus|sonnet|haiku|fable|claude-(opus|sonnet|haiku|fable)-[0-9]{1,2}(-[0-9]{1,2})?)$`)
	codexModelRE  = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{1,40}$`)
	codexModelKey = regexp.MustCompile(`(?m)^model\s*=\s*"[^"\n]*"\s*$`)
	policyMu      sync.Mutex
)

func defaultPolicy() Policy {
	return Policy{ThresholdPct: 85, ClaudeModel: "claude-haiku-5-5", CodexModel: "gpt-6-luna", IdleMinutes: 30, IdleMaxQuota: 50, IdleTarget: ""}
}

func (s *Service) policyPath() string { return filepath.Join(s.Store.dir, "policy.json") }

// LoadPolicy reads the policy (defaults when absent).
func (s *Service) LoadPolicy() Policy {
	p := defaultPolicy()
	if b, err := os.ReadFile(s.policyPath()); err == nil {
		_ = json.Unmarshal(b, &p)
	}
	if p.Switched == nil {
		p.Switched = map[string]string{}
	}
	return p
}

func (s *Service) savePolicy(p Policy) error {
	b, _ := json.MarshalIndent(p, "", "  ")
	if err := os.MkdirAll(s.Store.dir, 0o700); err != nil {
		return err
	}
	tmp := s.policyPath() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.policyPath())
}

// SetPolicy validates and stores the operator's settings.
func (s *Service) SetPolicy(enabled bool, threshold float64, claudeModel, codexModel string) (Policy, error) {
	if threshold < 30 || threshold > 99 {
		return Policy{}, &APIError{400, "AIAGENT_BAD_THRESHOLD", "threshold must be 30–99%"}
	}
	if !claudeModelRE.MatchString(claudeModel) {
		return Policy{}, &APIError{400, "AIAGENT_BAD_MODEL", "unknown Claude model"}
	}
	if !codexModelRE.MatchString(codexModel) {
		return Policy{}, &APIError{400, "AIAGENT_BAD_MODEL", "unknown Codex model"}
	}
	policyMu.Lock()
	defer policyMu.Unlock()
	p := s.LoadPolicy()
	now := time.Now().UTC()
	p.Enabled, p.ThresholdPct, p.ClaudeModel, p.CodexModel, p.ChangedAt = enabled, threshold, claudeModel, codexModel, &now
	return p, s.savePolicy(p)
}

func worstUsed(qs []Quota, provider string) (float64, bool) {
	worst, ok := 0.0, false
	for _, q := range qs {
		if q.ProviderID == provider && q.Confidence == "actual" && q.UsedPercent != nil {
			if !ok || *q.UsedPercent > worst {
				worst = *q.UsedPercent
			}
			ok = true
		}
	}
	return worst, ok
}

func sameModel(have, want string) bool {
	have, want = strings.ToLower(have), strings.ToLower(want)
	if have == want {
		return true
	}
	for _, f := range []string{"opus", "sonnet", "haiku", "fable"} {
		if strings.Contains(want, f) && strings.Contains(have, f) && (want == f || strings.HasPrefix(have, want)) {
			return true
		}
	}
	return false
}

func (s *Service) auditAuto(target, model, result, errText string) {
	if s.Auditor == nil {
		return
	}
	_ = s.Auditor.Write(audit.Record{Event: "result", RPC: "aiagent.autoModel", AdminSub: "policy", ParamsHash: paramsHash(target, model), Result: result, Err: errText})
}

// PolicyTick applies the policy once. Returns what it did (for tests/UI).
func (s *Service) PolicyTick(ctx context.Context) []string {
	policyMu.Lock()
	defer policyMu.Unlock()
	p := s.LoadPolicy()
	var did []string
	snap := s.Collector.Snapshot(ctx)
	note := func(msg string) {
		did = append(did, msg)
		now := time.Now().UTC()
		p.LastAction, p.LastActionAt = msg, &now
	}
	// ---- Claude sessions ----
	used, ok := worstUsed(snap.Quotas, "anthropic")
	switch {
	case p.Enabled && ok && used >= p.ThresholdPct:
		for _, ss := range snap.Sessions {
			if !ss.Live || ss.RuntimeType != "claudeCode" || ss.MessageVia == "" || ss.Kind == "sub" {
				continue
			}
			if _, done := p.Switched[ss.SessionID]; done || sameModel(ss.Model, p.ClaudeModel) {
				continue
			}
			_, err := s.Collector.SendToSession(ctx, ss.SessionID, "/model "+p.ClaudeModel)
			if err != nil {
				s.auditAuto(ss.SessionID, p.ClaudeModel, "error", err.Error())
				continue
			}
			s.auditAuto(ss.SessionID, p.ClaudeModel, "ok", "")
			p.Switched[ss.SessionID] = ss.Model
			note("Claude ใช้ไป " + pct(used) + " → " + ss.DisplayName + " เปลี่ยนเป็น " + p.ClaudeModel)
		}
	case len(p.Switched) > 0 && (!p.Enabled || !ok || used < p.ThresholdPct-restoreGap):
		for sid, prev := range p.Switched {
			live := false
			for _, ss := range snap.Sessions {
				live = live || (ss.SessionID == sid && ss.Live && ss.MessageVia != "")
			}
			if live && prev != "" && claudeModelRE.MatchString(aliasOf(prev)) {
				if _, err := s.Collector.SendToSession(ctx, sid, "/model "+aliasOf(prev)); err != nil {
					s.auditAuto(sid, prev, "error", err.Error())
					continue
				}
				s.auditAuto(sid, prev, "ok", "")
				note("คืน model เดิม (" + aliasOf(prev) + ") ให้ session " + shortID(sid))
			}
			delete(p.Switched, sid)
		}
	}
	// ---- Codex default for new sessions ----
	cused, cok := worstUsed(snap.Quotas, "openai")
	cfg := filepath.Join(s.Collector.serviceHome(), ".codex", "config.toml")
	switch {
	case p.Enabled && cok && cused >= p.ThresholdPct && p.CodexPrev == nil:
		prev, err := setCodexModel(cfg, p.CodexModel)
		if err == nil {
			p.CodexPrev = &prev
			s.auditAuto("codex-config", p.CodexModel, "ok", "")
			if prev != p.CodexModel {
				note("Codex ใช้ไป " + pct(cused) + " → session ใหม่ใช้ " + p.CodexModel)
			}
		} else {
			s.auditAuto("codex-config", p.CodexModel, "error", err.Error())
		}
	case p.CodexPrev != nil && (!p.Enabled || !cok || cused < p.ThresholdPct-restoreGap):
		if *p.CodexPrev != "" && *p.CodexPrev != p.CodexModel {
			if _, err := setCodexModel(cfg, *p.CodexPrev); err != nil {
				s.auditAuto("codex-config", *p.CodexPrev, "error", err.Error())
				break
			}
			s.auditAuto("codex-config", *p.CodexPrev, "ok", "")
			note("คืน model เดิมของ Codex (" + *p.CodexPrev + ")")
		}
		p.CodexPrev = nil
	}
	if msg := s.idleIdeas(ctx, &p, snap); msg != "" {
		note(msg)
	}
	_ = s.savePolicy(p)
	return did
}

// SetIdlePolicy stores the idle-ideas settings.
func (s *Service) SetIdlePolicy(enabled bool, minutes int, maxQuota float64, target string) (Policy, error) {
	if minutes < 10 || minutes > 24*60 {
		return Policy{}, &APIError{400, "AIAGENT_BAD_IDLE", "idle minutes must be 10–1440"}
	}
	if maxQuota < 5 || maxQuota > 95 {
		return Policy{}, &APIError{400, "AIAGENT_BAD_IDLE", "quota limit must be 5–95%"}
	}
	if !regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,40}$`).MatchString(target) {
		return Policy{}, &APIError{400, "AIAGENT_BAD_IDLE", "unknown workspace"}
	}
	policyMu.Lock()
	defer policyMu.Unlock()
	p := s.LoadPolicy()
	now := time.Now().UTC()
	p.IdleIdeas, p.IdleMinutes, p.IdleMaxQuota, p.IdleTarget, p.ChangedAt = enabled, minutes, maxQuota, target, &now
	return p, s.savePolicy(p)
}

// idleIdeas sends the brief when every condition holds; returns a note.
func (s *Service) idleIdeas(ctx context.Context, p *Policy, snap Snapshot) string {
	if !p.IdleIdeas || p.IdleTarget == "" {
		return ""
	}
	now := time.Now()
	if p.LastIdeaAt != nil && now.Sub(*p.LastIdeaAt) < ideaCooldown {
		return ""
	}
	cl, ok := worstUsed(snap.Quotas, "anthropic")
	if !ok || cl >= p.IdleMaxQuota {
		return "" // never on a guess: no actual Claude reading → no idle work
	}
	if cx, ok := worstUsed(snap.Quotas, "openai"); ok && cx >= p.IdleMaxQuota {
		return ""
	}
	for _, ss := range snap.Sessions {
		if !ss.Live || ss.DispatchVia != p.IdleTarget || ss.MessageVia == "" || ss.RuntimeType != "claudeCode" {
			continue
		}
		if ss.Status != StatusIdle && ss.Status != "completed" {
			return ""
		}
		if ss.LastEventAt == nil || now.Sub(*ss.LastEventAt) < time.Duration(p.IdleMinutes)*time.Minute {
			return ""
		}
		for _, r := range s.Collector.Reviews() {
			if r.Status == "open" && r.SessionID != "" && strings.HasPrefix(ss.SessionID, r.SessionID[:min(8, len(r.SessionID))]) {
				return "" // an earlier proposal is still waiting for the operator
			}
		}
		_, err := s.Collector.SendToSession(ctx, ss.SessionID, strings.ReplaceAll(IdeaBrief, "aiagent review", reviewBin()+" review"))
		res, errText := "ok", ""
		if err != nil {
			res, errText = "error", err.Error()
		}
		if s.Auditor != nil {
			_ = s.Auditor.Write(audit.Record{Event: "result", RPC: "aiagent.idleIdeas", AdminSub: "policy", ParamsHash: paramsHash(ss.SessionID), Result: res, Err: errText})
		}
		if err != nil {
			return ""
		}
		t := now.UTC()
		p.LastIdeaAt = &t
		return "ส่งงานช่วงว่างให้ " + ss.DisplayName + " (เสนอก่อน รออนุมัติ)"
	}
	return ""
}

func pct(f float64) string {
	return strings.TrimSuffix(strings.TrimSuffix(fmtFloat(f), ".0"), ".") + "%"
}

func fmtFloat(f float64) string {
	b, _ := json.Marshal(float64(int(f*10)) / 10)
	return string(b)
}

// aliasOf maps a reported model id (claude-opus-5-5) to a /model argument.
func aliasOf(id string) string {
	id = strings.ToLower(id)
	if claudeModelRE.MatchString(id) {
		return id
	}
	for _, f := range []string{"opus", "sonnet", "haiku", "fable"} {
		if strings.Contains(id, f) {
			return f
		}
	}
	return ""
}

// setCodexModel sets `model = "<m>"` in a Codex config.toml (top level),
// returning the previous value ("" when unset). Other lines are kept.
func setCodexModel(path, model string) (string, error) {
	if !codexModelRE.MatchString(model) {
		return "", &APIError{400, "AIAGENT_BAD_MODEL", "unknown Codex model"}
	}
	b, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	text := string(b)
	// only the top-level table: stop at the first [section]
	head, rest := text, ""
	if i := regexp.MustCompile(`(?m)^\[`).FindStringIndex(text); i != nil {
		head, rest = text[:i[0]], text[i[0]:]
	}
	prev := ""
	if m := codexModelKey.FindString(head); m != "" {
		prev = strings.Trim(strings.TrimSpace(strings.SplitN(m, "=", 2)[1]), `"`)
		head = codexModelKey.ReplaceAllString(head, `model = "`+model+`"`)
	} else {
		head = `model = "` + model + `"` + "\n" + head
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	mode := os.FileMode(0o600)
	if fi, err := os.Stat(path); err == nil {
		mode = fi.Mode().Perm()
	}
	tmp := path + ".aiagent.tmp"
	if err := os.WriteFile(tmp, []byte(head+rest), mode); err != nil {
		return "", err
	}
	return prev, os.Rename(tmp, path)
}

// StartPolicy runs PolicyTick every minute while the feature is enabled.
func (s *Service) StartPolicy(ctx context.Context) {
	go func() {
		t := time.NewTicker(time.Minute)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if s.Store.Enabled() {
					s.PolicyTick(ctx)
				}
			}
		}
	}()
}

// reviewBin is the command an agent is told to run to hand a review page
// back. The `review` subcommand belongs to this binary now, not to a separate
// host tool, so the running executable is the right answer: $AIAGENT_BIN when
// the operator has a reason to override it, else os.Executable(), else
// "aiagent" on PATH.
func reviewBin() string {
	if v := os.Getenv("AIAGENT_BIN"); v != "" {
		return v
	}
	if exe, err := os.Executable(); err == nil && exe != "" {
		return exe
	}
	return "aiagent"
}
