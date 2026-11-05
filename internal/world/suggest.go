// internal/aiagent/suggest.go
//
// Next-message suggestion like Claude Code's own prompt suggestion: a short
// Haiku call (no tools, not persisted) predicts what the operator will type
// next from the last exchange. One call per new agent message (cached), at
// most one at a time, skipped when the plan is near its limit.
package world

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const suggestPrompt = `คุณช่วยเติมข้อความของผู้ใช้ในแชตกับ AI agent (เหมือน prompt suggestion ของ Claude Code).
อ่านบทสนทนาล่าสุดด้านล่าง แล้วเดา "ข้อความถัดไป" ที่ผู้ใช้น่าจะพิมพ์ส่ง:
- สั้น 1 บรรทัด ภาษาเดียวกับผู้ใช้ เขียนเป็นคำสั่งหรือคำตอบที่ส่งได้ทันที
- เจาะจงกับสิ่งที่ agent เสนอให้ทำต่อ ("ถัดไป", "needs input", คำถาม) เช่น "ทำรอบปรับหน้าตาบ้านต่อเลย"
- ห้ามทวนหรือก๊อปข้อความผู้ใช้ล่าสุด (เรื่องนั้นทำไปแล้ว) ให้มองไปที่ขั้นถัดไปที่ agent เพิ่งเสนอ
- ห้ามอธิบาย ห้ามใส่เครื่องหมายคำพูด ตอบเฉพาะข้อความนั้น`

type suggester struct {
	mu    sync.Mutex
	busy  bool
	cache map[string]string // hash(last agent msg) → suggestion
}

var sug = &suggester{cache: map[string]string{}}

// Suggest returns the predicted next message for a session ("" when none).
func (c *Collector) Suggest(ctx context.Context, id string) (string, error) {
	msgs, err := c.Messages(ctx, id, 8)
	if err != nil {
		return "", err
	}
	var lastUser, lastAgent string
	for i := len(msgs) - 1; i >= 0 && (lastUser == "" || lastAgent == ""); i-- {
		if msgs[i].Role == "assistant" && lastAgent == "" {
			lastAgent = msgs[i].Text
		}
		if msgs[i].Role == "user" && lastUser == "" && lastAgent != "" {
			lastUser = msgs[i].Text
		}
	}
	if lastAgent == "" {
		return "", nil
	}
	h := sha256.Sum256([]byte(lastAgent))
	key := hex.EncodeToString(h[:8])
	sug.mu.Lock()
	if v, ok := sug.cache[key]; ok {
		sug.mu.Unlock()
		return v, nil
	}
	if sug.busy {
		sug.mu.Unlock()
		return "", nil // another prediction is running; the UI asks again
	}
	sug.busy = true
	sug.mu.Unlock()
	defer func() { sug.mu.Lock(); sug.busy = false; sug.mu.Unlock() }()

	if used, ok := worstUsed(c.Snapshot(ctx).Quotas, "anthropic"); ok && used >= 90 {
		return "", nil
	}
	in := "ผู้ใช้ล่าสุด:\n" + tailRunes(lastUser, 800) + "\n\nagent ล่าสุด:\n" + tailRunes(lastAgent, 2500)
	dir := c.suggestDir()
	cctx, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()
	out, err := c.runIn(cctx, dir, in, c.ClaudePath, "-p", "--model", "claude-haiku-5-5", "--tools", "", "--no-session-persistence", suggestPrompt)
	if err != nil {
		return "", nil
	}
	v := strings.TrimSpace(strings.Split(strings.TrimSpace(out), "\n")[0])
	v = strings.Trim(v, "\"'“”")
	if strings.TrimSpace(v) == strings.TrimSpace(lastUser) {
		return "", nil // a repeat of what was just sent is no suggestion
	}
	if v == "" || len([]rune(v)) > 160 {
		return "", nil
	}
	v = Redact(v)
	sug.mu.Lock()
	if len(sug.cache) > 200 {
		sug.cache = map[string]string{}
	}
	sug.cache[key] = v
	sug.mu.Unlock()
	return v, nil
}

func tailRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return "…" + string(r[len(r)-n:])
}

// suggestDir is a private working dir so the one-shot run never lands in a
// real project.
func (c *Collector) suggestDir() string {
	d := os.Getenv("AIAGENT_SUGGEST_DIR")
	if d == "" {
		d = filepath.Join(DefaultStateDir(), "suggest")
	}
	_ = os.MkdirAll(d, 0o700)
	return d
}
