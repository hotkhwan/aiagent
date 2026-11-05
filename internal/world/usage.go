// internal/aiagent/usage.go
//
// Claude plan usage. Claude Code reports the account's rate-limit windows
// (5-hour and 7-day, used_percentage + resets_at) only to the status line
// command. `aiagent statusline` is that command: it prints a short
// status line and saves just those numbers (plus the model name) to
// ~/.claude/aiagent-usage.json, which the collector reads. Nothing else from the
// status line input (cwd, transcript path, cost) is kept.
package world

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// UsageWindow is one provider-reported rate-limit window.
type UsageWindow struct {
	UsedPercent float64    `json:"usedPercent"`
	ResetsAt    *time.Time `json:"resetsAt,omitempty"`
}

// ClaudeUsage is the last reading saved by the status line command.
type ClaudeUsage struct {
	ObservedAt time.Time    `json:"observedAt"`
	Model      string       `json:"model,omitempty"`
	FiveHour   *UsageWindow `json:"fiveHour,omitempty"`
	SevenDay   *UsageWindow `json:"sevenDay,omitempty"`
}

const usageFile = "aiagent-usage.json"

// UsagePath is where a home's reading is kept.
func UsagePath(home string) string { return filepath.Join(home, ".claude", usageFile) }

type statusInput struct {
	Model *struct {
		ID          string `json:"id"`
		DisplayName string `json:"display_name"`
	} `json:"model"`
	RateLimits map[string]json.RawMessage `json:"rate_limits"`
}

func parseWindow(raw json.RawMessage) *UsageWindow {
	var w map[string]json.RawMessage
	if json.Unmarshal(raw, &w) != nil {
		return nil
	}
	var used *float64
	for _, k := range []string{"used_percentage", "used_percent", "utilization"} {
		var f float64
		if v, ok := w[k]; ok && json.Unmarshal(v, &f) == nil {
			used = &f
			break
		}
	}
	if used == nil || math.IsNaN(*used) || *used < 0 {
		return nil
	}
	out := &UsageWindow{UsedPercent: math.Min(*used, 100)}
	if v, ok := w["resets_at"]; ok {
		var n float64
		var s string
		switch {
		case json.Unmarshal(v, &n) == nil && n > 0:
			if n > 1e12 { // milliseconds
				n /= 1000
			}
			t := time.Unix(int64(n), 0).UTC()
			out.ResetsAt = &t
		case json.Unmarshal(v, &s) == nil:
			if t, err := time.Parse(time.RFC3339, s); err == nil {
				t = t.UTC()
				out.ResetsAt = &t
			}
		}
	}
	return out
}

// RunStatusline reads Claude Code's status line JSON from in, saves the
// usage reading under home and writes the status line to out.
func RunStatusline(in io.Reader, out io.Writer, home string, now time.Time) error {
	var si statusInput
	if err := json.NewDecoder(io.LimitReader(in, 1<<20)).Decode(&si); err != nil {
		fmt.Fprintln(out, "aiagent")
		return err
	}
	u := ClaudeUsage{ObservedAt: now.UTC()}
	if si.Model != nil {
		u.Model = truncateRunes(firstNonEmpty(si.Model.DisplayName, si.Model.ID), 60)
	}
	u.FiveHour = parseWindow(si.RateLimits["five_hour"])
	u.SevenDay = parseWindow(si.RateLimits["seven_day"])
	parts := []string{}
	if u.Model != "" {
		parts = append(parts, u.Model)
	}
	if u.FiveHour != nil {
		parts = append(parts, fmt.Sprintf("5h %.0f%%", u.FiveHour.UsedPercent))
	}
	if u.SevenDay != nil {
		parts = append(parts, fmt.Sprintf("7d %.0f%%", u.SevenDay.UsedPercent))
	}
	if len(parts) == 0 {
		parts = append(parts, "aiagent")
	}
	fmt.Fprintln(out, strings.Join(parts, " · "))
	if u.FiveHour == nil && u.SevenDay == nil {
		return nil // nothing worth saving (e.g. API-key sessions have no plan windows)
	}
	b, _ := json.Marshal(u)
	path := UsagePath(home)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".aiagent-usage-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

// claudeQuotas turns the freshest saved reading into quota rows. Never
// estimated: a missing reading is "unknown", a window whose reset passed is
// "stale" with no percentage.
func (c *Collector) claudeQuotas(now time.Time) []Quota {
	var best *ClaudeUsage
	var switched bool
	for _, h := range c.homes() {
		b, err := os.ReadFile(UsagePath(h))
		if err != nil {
			continue
		}
		var u ClaudeUsage
		if json.Unmarshal(b, &u) == nil && (best == nil || u.ObservedAt.After(best.ObservedAt)) {
			best = &u
			// a reading taken before the credentials changed belongs to the
			// previous account (account switch): do not show it as current
			fi, err := os.Stat(filepath.Join(h, ".claude", ".credentials.json"))
			switched = err == nil && fi.ModTime().After(u.ObservedAt)
		}
	}
	if best != nil && switched {
		return []Quota{{ProviderID: "anthropic", Mode: "guarded", Confidence: "unknown",
			Source: "account changed; waiting for the first reading from the new account"}}
	}
	if best == nil {
		return []Quota{{ProviderID: "anthropic", Mode: "guarded", Confidence: "unknown",
			Source: "no reading yet: set Claude Code's status line to `aiagent statusline`"}}
	}
	var out []Quota
	add := func(w *UsageWindow, minutes int, label string) {
		if w == nil {
			return
		}
		at := best.ObservedAt
		q := Quota{ProviderID: "anthropic", WindowMinutes: minutes, ObservedAt: &at, ResetsAt: w.ResetsAt, Source: "claude status line " + label}
		if w.ResetsAt != nil && now.After(*w.ResetsAt) {
			q.Confidence, q.Mode = "stale", "guarded"
		} else {
			used := w.UsedPercent
			rem := 100 - used
			q.UsedPercent, q.RemainingPercent = &used, &rem
			q.Confidence, q.Mode = "actual", quotaMode(rem)
		}
		out = append(out, q)
	}
	add(best.FiveHour, 300, "5-hour")
	add(best.SevenDay, 10080, "7-day")
	return out
}
