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
	// SentAt/SentVia/Delivery record an answer typed into the session: the
	// channel ("relay aiagent-…", "… (resume)", "queued") and whether its
	// transcript showed it arrive (acked | unknown).
	SentAt   *time.Time `json:"sentAt,omitempty"`
	SentVia  string     `json:"sentVia,omitempty"`
	Delivery string     `json:"delivery,omitempty"`
	// Reply is the answer that was sent, kept so a send that was never
	// confirmed can be sent again as it was.
	Reply string `json:"reply,omitempty"`
	Size  int64  `json:"size"`
	// Owner is the room a review was filed in, when the admin lists rooms' reviews.
	Owner string `json:"owner,omitempty"`
	home  string
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
		return nil, ErrNotFound("ไม่พบงานรีวิวนี้")
	}
	for _, r := range c.Reviews() {
		if r.ID == id {
			r := r
			return &r, nil
		}
	}
	return nil, ErrNotFound("ไม่พบงานรีวิวนี้")
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
		return "", "", &APIError{400, "AIAGENT_ITEMS", "ต้องตัดสินให้ครบทุกข้อในหน้า"}
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
			return "", "", &APIError{400, "AIAGENT_NOTE_TOO_LONG", "หมายเหตุยาวเกินไป"}
		}
		switch it.Verdict {
		case "approve":
			lines = append(lines, fmt.Sprintf("%d. %s — ผ่าน", i+1, title))
		case "revise":
			if n == "" {
				return "", "", &APIError{400, "AIAGENT_NOTE_REQUIRED", fmt.Sprintf("ต้องบอกว่าให้แก้อะไรในข้อ %d", i+1)}
			}
			decision = "revise"
			lines = append(lines, fmt.Sprintf("%d. %s — ต้องแก้ไข: %s", i+1, title, n))
		default:
			return "", "", &APIError{400, "AIAGENT_BAD_DECISION", "ทุกข้อต้องเป็น ผ่าน หรือ ต้องแก้ไข"}
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
		return "", "", &APIError{400, "AIAGENT_NOTE_TOO_LONG", "หมายเหตุยาวเกินไป"}
	}
	return composeItems(r, items, note)
}

// ReviewSent is how an answer went to the session.
type ReviewSent struct {
	Via      string // "relay aiagent-…", "… (resume)", "queued"
	Delivery string // DeliveryAcked | DeliveryUnknown; empty when queued
	Queued   bool   // the agent was busy: the outbox types it in later
}

// ReviewDeliver types an answer into the review's session. The session is
// always the one stored on the review, never one a client names.
type ReviewDeliver func(ctx context.Context, r *Review, reply string) (ReviewSent, error)

// DecideReview records the operator's answer and, when deliver is set, sends
// the reply to the review's session first; a failed send leaves the review
// open so it can still be copied. With items, the decision follows from the
// per-section verdicts; without, decision is approve or revise.
func (c *Collector) DecideReview(ctx context.Context, id, decision, note string, items []ReviewItem, deliver ReviewDeliver) (string, *ReviewSent, error) {
	note = strings.TrimSpace(note)
	if len(note) > 4000 {
		return "", nil, &APIError{400, "AIAGENT_NOTE_TOO_LONG", "หมายเหตุยาวเกินไป"}
	}
	// One answer at a time per review: two presses (two tabs, the admin and
	// the room's user) both found it open and both typed the reply.
	defer c.lockSend("review:" + id)()
	r, err := c.review(id)
	if err != nil {
		return "", nil, err
	}
	var reply string
	if len(items) > 0 {
		if decision, reply, err = composeItems(r, items, note); err != nil {
			return "", nil, err
		}
	} else {
		if decision != "approve" && decision != "revise" {
			return "", nil, &APIError{400, "AIAGENT_BAD_DECISION", "ผลตรวจต้องเป็น ผ่าน หรือ ต้องแก้ไข"}
		}
		if decision == "revise" && note == "" {
			return "", nil, &APIError{400, "AIAGENT_NOTE_REQUIRED", "ต้องบอกว่าให้แก้อะไร"}
		}
		reply = ReviewReply(r, decision, note)
	}
	if r.Status != "open" {
		// already answered: never type the reply into the session twice;
		// copying it again is fine and changes nothing
		if deliver != nil {
			if reviewUnconfirmed(r) {
				return "", nil, &APIError{409, reviewUnconfirmedCode, reviewUnconfirmedMsg}
			}
			return "", nil, ErrConflict("งานรีวิวนี้ตอบไปแล้ว (" + r.Status + ")")
		}
		return reply, nil, nil
	}
	var sent *ReviewSent
	now := c.now().UTC()
	if deliver != nil {
		if r.SessionID == "" {
			return "", nil, ErrConflict("งานรีวิวนี้ไม่มี session ให้คัดลอกคำตอบแทน")
		}
		s, err := deliver(ctx, r, reply)
		if err != nil {
			return "", nil, err
		}
		sent = &s
		r.SentAt, r.SentVia, r.Delivery, r.Reply = &now, s.Via, s.Delivery, reply
	}
	r.Status = map[string]string{"approve": "approved", "revise": "revise"}[decision]
	r.Note, r.DecidedAt = truncateRunes(note, 4000), &now
	if err := writeReviewMeta(r.home, r); err != nil {
		return "", nil, err
	}
	return reply, sent, nil
}

const (
	reviewUnconfirmedCode = "AIAGENT_REVIEW_UNCONFIRMED"
	reviewUnconfirmedMsg  = "ผลตรวจนี้พิมพ์ให้ agent ไปแล้วแต่ยังไม่ยืนยันว่าถึง ถ้าเปิดดูแล้วไม่ถึงให้กด \"ส่งซ้ำ\""
)

// reviewUnconfirmed reports an answered review whose reply was typed but
// never seen in the session's transcript: it may not have reached the agent,
// so the operator may send it again, on purpose.
func reviewUnconfirmed(r *Review) bool {
	return r.Status != "open" && r.SentAt != nil && r.Delivery == DeliveryUnknown && r.SentVia != "queued"
}

// ResendReview sends an answered review's reply again, only when its first
// send was not confirmed and only because the operator asked (resend).
func (c *Collector) ResendReview(ctx context.Context, id string, deliver ReviewDeliver) (string, *ReviewSent, error) {
	defer c.lockSend("review:" + id)()
	r, err := c.review(id)
	if err != nil {
		return "", nil, err
	}
	if r.Status == "open" {
		return "", nil, ErrConflict("งานรีวิวนี้ยังไม่ได้ตอบ ให้ตรวจแล้วกดส่ง")
	}
	if !reviewUnconfirmed(r) || r.Reply == "" {
		return "", nil, ErrConflict("งานรีวิวนี้ตอบไปแล้ว (" + r.Status + ")")
	}
	s, err := deliver(ctx, r, r.Reply)
	if err != nil {
		return "", nil, err
	}
	now := c.now().UTC()
	r.SentAt, r.SentVia, r.Delivery = &now, s.Via, s.Delivery
	if err := writeReviewMeta(r.home, r); err != nil {
		return "", nil, err
	}
	return r.Reply, &s, nil
}

// reopenReview gives an answered review back to the operator when the reply
// queued for it was refused later (a question on the agent's screen, the
// session gone): nothing reached the agent, so it must not count as sent.
func (c *Collector) reopenReview(id string) {
	defer c.lockSend("review:" + id)()
	r, err := c.review(id)
	if err != nil || r.Status == "open" || r.SentVia != "queued" {
		return
	}
	r.Status, r.DecidedAt, r.SentAt, r.SentVia, r.Delivery, r.Reply = "open", nil, nil, "", "", ""
	_ = writeReviewMeta(r.home, r)
}

var sessionUUIDRE = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// SendReview types reply into the review's session and returns that
// session's full id and the channel used. A running session gets it through
// its normal channel. A Claude Code session of this machine that is not
// running any more is brought back as a background job in its folder (as the
// web's resume does) and given it there.
func (c *Collector) SendReview(ctx context.Context, r *Review, reply string) (string, string, error) {
	sid, err := c.resolveSession(ctx, r.SessionID)
	if err == nil {
		via, err := c.SendToSession(ctx, sid, reply)
		return sid, via, err
	}
	if ae, ok := err.(*APIError); !ok || ae.Status != 404 {
		return "", "", err // ambiguous id
	}
	if r.Runtime != "claudeCode" || !sessionUUIDRE.MatchString(r.SessionID) || r.home != c.serviceHome() {
		return "", "", ErrNotFound("session ของงานรีวิวนี้ไม่ได้รันอยู่และเปิดต่อจากที่นี่ไม่ได้ ให้คัดลอกคำตอบไปวางใน session")
	}
	if m, _ := filepath.Glob(filepath.Join(r.home, ".claude", "projects", "*", r.SessionID+".jsonl")); len(m) == 0 {
		return "", "", ErrNotFound("session ของงานรีวิวนี้ไม่ได้รันอยู่และบทสนทนาหายไปแล้ว ให้คัดลอกคำตอบแทน")
	}
	reply, err = cleanText(reply)
	if err != nil {
		return "", "", err
	}
	short := shortID(r.SessionID)
	for _, o := range c.Snapshot(ctx).Sessions {
		if o.Live && strings.HasPrefix(o.SessionID, short) {
			return "", "", ErrConflict("รหัสย่อ " + short + " ซ้ำกับ session อื่น")
		}
	}
	ss := &Session{SessionID: r.SessionID, RuntimeType: "claudeCode", Cwd: r.Cwd, Kind: "bg", AgentState: "done", home: r.home}
	if err := c.wakeInBackground(ctx, ss, c.resumeDir(ss)); err != nil {
		return "", "", err
	}
	if err := c.relaySend(ctx, ss, reply); err != nil {
		// no id: a resumed CLI that shows no prompt is not a busy agent
		// the outbox could wait for
		return "", "", err
	}
	return r.SessionID, "relay " + relayName(short) + " (resume)", nil
}

// resolveSession accepts a full id or a unique prefix of a live session.
func (c *Collector) resolveSession(ctx context.Context, id string) (string, error) {
	var hit string
	for _, s := range c.Snapshot(ctx).Sessions {
		if s.Live && strings.HasPrefix(s.SessionID, id) {
			if hit != "" {
				return "", ErrConflict("รหัส session " + id + " ซ้ำกับ session อื่น")
			}
			hit = s.SessionID
		}
	}
	if hit == "" {
		return "", ErrNotFound("session ของงานรีวิวนี้ไม่ได้รันอยู่ ให้คัดลอกคำตอบแทน")
	}
	return hit, nil
}
