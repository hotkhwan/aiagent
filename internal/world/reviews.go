// internal/aiagent/reviews.go
//
// Review pages: a stand-in for Claude artifacts that also works for Codex.
// An agent writes an HTML page describing what it did or changed and files
// it with `aiagent review --title ... page.html`. AI Agent World lists
// open reviews in the Testing room, shows the page in a sandboxed frame, and
// the operator answers "approve / close" or "needs changes" — as text to copy
// or send straight back to the session.
package world

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Review is one filed page.
type Review struct {
	ID        string     `json:"id"`
	Title     string     `json:"title"`
	Summary   string     `json:"summary,omitempty"`
	SessionID string     `json:"sessionId,omitempty"`
	Runtime   string     `json:"runtime,omitempty"` // claudeCode | codexCli
	Cwd       string     `json:"cwd,omitempty"`
	CreatedAt time.Time  `json:"createdAt"`
	Status    string     `json:"status"` // open | approved | revise
	Note      string     `json:"note,omitempty"`
	DecidedAt *time.Time `json:"decidedAt,omitempty"`
	Size      int64      `json:"size"`
	home      string
}

const (
	reviewDir     = "aiagent-reviews"
	reviewMaxHTML = 3 << 20
	reviewKeep    = 30 * 24 * time.Hour
)

var reviewIDRE = regexp.MustCompile(`^r[0-9]{8}-[0-9]{6}-[0-9a-f]{8}$`)

func reviewPath(home, id, ext string) string {
	return filepath.Join(home, ".claude", reviewDir, id+ext)
}

// FileReview stores page under home and returns its record. Called by the
// CLI as the agent's own user.
func FileReview(home, title, summary, sessionID, runtime, cwd string, page []byte, now time.Time) (*Review, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return nil, fmt.Errorf("--title is required")
	}
	if len(page) == 0 || len(page) > reviewMaxHTML {
		return nil, fmt.Errorf("page must be 1 byte to %d MB", reviewMaxHTML>>20)
	}
	var rb [4]byte
	_, _ = rand.Read(rb[:])
	r := &Review{
		ID:        "r" + now.UTC().Format("20060102-150405") + "-" + hex.EncodeToString(rb[:]),
		Title:     truncateRunes(Redact(title), 140),
		Summary:   truncateRunes(Redact(strings.TrimSpace(summary)), 1200),
		SessionID: truncateRunes(sessionID, 80),
		Runtime:   runtime,
		Cwd:       cwd,
		CreatedAt: now.UTC(),
		Status:    "open",
		Size:      int64(len(page)),
	}
	dir := filepath.Join(home, ".claude", reviewDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	if err := os.WriteFile(reviewPath(home, r.ID, ".html"), page, 0o600); err != nil {
		return nil, err
	}
	return r, writeReviewMeta(home, r)
}

func writeReviewMeta(home string, r *Review) error {
	b, _ := json.MarshalIndent(r, "", "  ")
	tmp := reviewPath(home, r.ID, ".json.tmp")
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, reviewPath(home, r.ID, ".json"))
}

// Reviews lists filed reviews of every scanned home, newest first, and
// prunes decided ones older than reviewKeep.
func (c *Collector) Reviews() []Review {
	now := c.now()
	var out []Review
	for _, h := range c.homes() {
		metas, _ := filepath.Glob(filepath.Join(h, ".claude", reviewDir, "r*.json"))
		for _, m := range metas {
			b, err := os.ReadFile(m)
			if err != nil {
				continue
			}
			var r Review
			if json.Unmarshal(b, &r) != nil || !reviewIDRE.MatchString(r.ID) {
				continue
			}
			if r.Status != "open" && r.DecidedAt != nil && now.Sub(*r.DecidedAt) > reviewKeep {
				_ = os.Remove(m)
				_ = os.Remove(reviewPath(h, r.ID, ".html"))
				continue
			}
			r.home = h
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out
}

func (c *Collector) review(id string) (*Review, error) {
	if !reviewIDRE.MatchString(id) {
		return nil, ErrNotFound("no such review")
	}
	for _, r := range c.Reviews() {
		if r.ID == id {
			r := r
			return &r, nil
		}
	}
	return nil, ErrNotFound("no such review")
}

// ReviewPage returns the stored HTML of a review.
func (c *Collector) ReviewPage(id string) ([]byte, error) {
	r, err := c.review(id)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(reviewPath(r.home, r.ID, ".html"))
}

// ReviewReply is the text an answer produces for the agent.
func ReviewReply(r *Review, decision, note string) string {
	note = strings.TrimSpace(note)
	var b strings.Builder
	if decision == "approve" {
		fmt.Fprintf(&b, "✅ ตรวจแล้ว ตกลง ปิดงานได้: %s (review %s)", r.Title, r.ID)
		if note != "" {
			fmt.Fprintf(&b, "\nหมายเหตุ: %s", note)
		}
	} else {
		fmt.Fprintf(&b, "✏️ ตรวจแล้ว ขอแก้ไขเพิ่มเติม: %s (review %s)\n%s", r.Title, r.ID, note)
		b.WriteString("\nแก้แล้วส่งหน้าตรวจใหม่ด้วย aiagent review")
	}
	return b.String()
}

// ReviewItem is one section of a review page and the operator's verdict on it.
type ReviewItem struct {
	Title   string `json:"title"`
	Verdict string `json:"verdict"` // approve | revise
	Note    string `json:"note,omitempty"`
}

const reviewMaxItems = 50

// composeItems turns per-section verdicts into the decision and the reply the
// agent gets. Any revise makes the whole answer a revise.
func composeItems(r *Review, items []ReviewItem, note string) (string, string, error) {
	if len(items) == 0 || len(items) > reviewMaxItems {
		return "", "", &APIError{400, "AIAGENT_ITEMS", "judge every section of the page"}
	}
	decision := "approve"
	lines := make([]string, 0, len(items))
	for i, it := range items {
		title := truncateRunes(Redact(strings.TrimSpace(it.Title)), 200)
		if title == "" {
			title = fmt.Sprintf("ข้อ %d", i+1)
		}
		n := strings.TrimSpace(it.Note)
		if len(n) > 4000 {
			return "", "", &APIError{400, "AIAGENT_NOTE_TOO_LONG", "note is too long"}
		}
		switch it.Verdict {
		case "approve":
			lines = append(lines, fmt.Sprintf("%d. %s — ผ่าน", i+1, title))
		case "revise":
			if n == "" {
				return "", "", &APIError{400, "AIAGENT_NOTE_REQUIRED", fmt.Sprintf("say what to change in item %d", i+1)}
			}
			decision = "revise"
			lines = append(lines, fmt.Sprintf("%d. %s — ต้องแก้ไข: %s", i+1, title, n))
		default:
			return "", "", &APIError{400, "AIAGENT_BAD_DECISION", "every section needs approve or revise"}
		}
	}
	var b strings.Builder
	if decision == "approve" {
		fmt.Fprintf(&b, "✅ ตรวจแล้ว ตกลง ปิดงานได้: %s (review %s)", r.Title, r.ID)
	} else {
		fmt.Fprintf(&b, "✏️ ตรวจแล้ว ขอแก้ไขเพิ่มเติม: %s (review %s)", r.Title, r.ID)
	}
	b.WriteString("\n\n" + strings.Join(lines, "\n"))
	if note = strings.TrimSpace(note); note != "" {
		fmt.Fprintf(&b, "\n\nหมายเหตุ: %s", note)
	}
	if decision == "revise" {
		b.WriteString("\n\nแก้ตามข้อที่ขอแก้ แล้วส่งหน้าตรวจใหม่ด้วย aiagent review")
	}
	return decision, b.String(), nil
}

// ComposeReview previews the reply for per-section verdicts. It changes nothing.
func (c *Collector) ComposeReview(id string, items []ReviewItem, note string) (string, string, error) {
	r, err := c.review(id)
	if err != nil {
		return "", "", err
	}
	if len(strings.TrimSpace(note)) > 4000 {
		return "", "", &APIError{400, "AIAGENT_NOTE_TOO_LONG", "note is too long"}
	}
	return composeItems(r, items, note)
}

// DecideReview records the operator's answer and, when send is set, types the
// reply into the session through its normal channel. With items, the decision
// follows from the per-section verdicts; without, decision is approve or revise.
func (c *Collector) DecideReview(ctx context.Context, id, decision, note string, items []ReviewItem, send bool) (string, string, error) {
	note = strings.TrimSpace(note)
	if len(note) > 4000 {
		return "", "", &APIError{400, "AIAGENT_NOTE_TOO_LONG", "note is too long"}
	}
	r, err := c.review(id)
	if err != nil {
		return "", "", err
	}
	var reply string
	if len(items) > 0 {
		if decision, reply, err = composeItems(r, items, note); err != nil {
			return "", "", err
		}
	} else {
		if decision != "approve" && decision != "revise" {
			return "", "", &APIError{400, "AIAGENT_BAD_DECISION", "decision must be approve or revise"}
		}
		if decision == "revise" && note == "" {
			return "", "", &APIError{400, "AIAGENT_NOTE_REQUIRED", "say what to change"}
		}
		reply = ReviewReply(r, decision, note)
	}
	if r.Status != "open" {
		// already answered: never type the reply into the session twice;
		// copying it again is fine and changes nothing
		if send {
			return "", "", ErrConflict("this review was already answered (" + r.Status + ")")
		}
		return reply, "", nil
	}
	via := ""
	if send {
		if r.SessionID == "" {
			return "", "", ErrConflict("this review has no session; copy the reply instead")
		}
		sid, err := c.resolveSession(ctx, r.SessionID)
		if err != nil {
			return "", "", err
		}
		if via, err = c.SendToSession(ctx, sid, reply); err != nil {
			return "", "", err
		}
	}
	now := c.now().UTC()
	r.Status = map[string]string{"approve": "approved", "revise": "revise"}[decision]
	r.Note, r.DecidedAt = truncateRunes(note, 4000), &now
	if err := writeReviewMeta(r.home, r); err != nil {
		return "", "", err
	}
	return reply, via, nil
}

// resolveSession accepts a full id or a unique prefix of a live session.
func (c *Collector) resolveSession(ctx context.Context, id string) (string, error) {
	var hit string
	for _, s := range c.Snapshot(ctx).Sessions {
		if s.Live && strings.HasPrefix(s.SessionID, id) {
			if hit != "" {
				return "", ErrConflict("session id " + id + " is ambiguous")
			}
			hit = s.SessionID
		}
	}
	if hit == "" {
		return "", ErrNotFound("the session of this review is not running; copy the reply instead")
	}
	return hit, nil
}
