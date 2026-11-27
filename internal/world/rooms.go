// internal/world/rooms.go
//
// The model every room runs. Users cannot choose it: rooms start with the
// model named in a root-owned file (agent-loop.sh passes it to claude), the
// model route is closed to them, and only a short list of slash commands may
// be typed into their agent. The admin sets one rule: the latest Opus, and the
// economy model while the account's weekly usage is above a threshold.
//
// Usage is what Claude Code reported to a status line, never an estimate; with
// no reading the rooms stay on the main model. A room is switched by
// restarting its agent, which continues the same conversation, and only while
// that agent is idle.
package world

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/gofiber/fiber/v3"

	"github.com/hotkhwan/aiagent/internal/audit"
)

const (
	roomsFile      = "rooms.json"
	roomMainModel  = "opus" // the CLI's alias for the latest Opus
	roomLowModel   = "claude-haiku-5-5"
	roomRestoreGap = 5
	weeklyMinutes  = 10080
	roomQuiet      = time.Minute
)

// RoomPolicy is the admin's rule for the rooms' model.
type RoomPolicy struct {
	Enabled   bool    `json:"enabled"`
	WeeklyPct float64 `json:"weeklyPct"`
}

func (s *Service) loadRoomPolicy() RoomPolicy {
	p := RoomPolicy{Enabled: true, WeeklyPct: 80}
	if b, err := os.ReadFile(filepath.Join(s.Store.dir, roomsFile)); err == nil {
		_ = json.Unmarshal(b, &p)
	}
	return p
}

func (s *Service) saveRoomPolicy(p RoomPolicy) error {
	if !(p.WeeklyPct >= 10 && p.WeeklyPct <= 99) {
		return ErrBadRequest("เกณฑ์รายสัปดาห์ต้องเป็น 10-99%")
	}
	b, err := json.Marshal(p)
	if err != nil {
		return err
	}
	tmp := filepath.Join(s.Store.dir, roomsFile+".tmp")
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(s.Store.dir, roomsFile))
}

// roomModel decides the model from the weekly reading. The switch back waits
// for a few points less, so a reading on the line does not flap.
func roomModel(p RoomPolicy, weekly float64, known bool, current string) string {
	if !p.Enabled || !known {
		return roomMainModel
	}
	limit := p.WeeklyPct
	if current == roomLowModel {
		limit -= roomRestoreGap
	}
	if weekly > limit {
		return roomLowModel
	}
	return roomMainModel
}

// weeklyUsage is the freshest actual 7-day reading of this service and the
// rooms, which share one account.
func (s *Service) weeklyUsage(ctx context.Context) (float64, bool) {
	var best *Quota
	consider := func(qs []Quota) {
		for i := range qs {
			q := &qs[i]
			if q.ProviderID != "anthropic" || q.WindowMinutes != weeklyMinutes || q.Confidence != "actual" || q.UsedPercent == nil || q.ObservedAt == nil {
				continue
			}
			if best == nil || q.ObservedAt.After(*best.ObservedAt) {
				best = q
			}
		}
	}
	consider(s.Collector.Snapshot(ctx).Quotas)
	for _, name := range s.roomNames() {
		consider(s.instance(ctx, name).quotas)
	}
	if best == nil {
		return 0, false
	}
	return *best.UsedPercent, true
}

func (s *Service) roomNames() []string {
	st, err := s.Store.Load()
	if err != nil {
		return nil
	}
	var out []string
	for _, u := range st.Users {
		if u.OSUser != "" && u.Hash != "" {
			out = append(out, u.OSUser)
		}
	}
	return out
}

func (o *OSUsers) modelPath() string { return filepath.Join(o.ModelDir, "model") }

func (o *OSUsers) currentModel() (string, time.Time) {
	b, err := os.ReadFile(o.modelPath())
	if err != nil {
		return "", time.Time{}
	}
	fi, err := os.Stat(o.modelPath())
	if err != nil {
		return "", time.Time{}
	}
	return strings.TrimSpace(string(b)), fi.ModTime()
}

func modelMatches(have, want string) bool {
	family := func(m string) string {
		for _, f := range claudeFamilies {
			if strings.Contains(m, f) {
				return f
			}
		}
		return m
	}
	return family(have) == family(want)
}

// needsModel says whether a room's session must be restarted to run want.
// After a restart the last reply is older than the process, so a session is
// not restarted again before it has answered on the new model.
func needsModel(ss Session, want string, changed time.Time) bool {
	if !ss.Live || ss.StartedAt == nil {
		return false
	}
	if ss.StartedAt.Before(changed) {
		return true
	}
	return ss.Model != "" && !modelMatches(ss.Model, want) && ss.LastEventAt != nil && ss.LastEventAt.After(*ss.StartedAt)
}

// applyRoomModel runs once a minute.
func (s *Service) applyRoomModel(ctx context.Context, now time.Time) {
	o := s.OSUsers
	cur, changed := o.currentModel()
	weekly, known := s.weeklyUsage(ctx)
	want := roomModel(s.loadRoomPolicy(), weekly, known, cur)
	if want != cur {
		rec := audit.Record{Event: "intent", RPC: "aiagent.roomModel", AdminSub: "policy", ParamsHash: paramsHash(want)}
		if err := s.Auditor.Write(rec); err != nil {
			return
		}
		tmp := o.modelPath() + ".tmp"
		if err := os.WriteFile(tmp, []byte(want+"\n"), 0o644); err != nil {
			log.Printf("room model: %v", err)
			return
		}
		if err := os.Rename(tmp, o.modelPath()); err != nil {
			log.Printf("room model: %v", err)
			return
		}
		_, changed = o.currentModel()
	}
	for _, name := range s.roomNames() {
		for _, ss := range s.instance(ctx, name).sessions {
			if ss.DispatchVia != name || !needsModel(ss, want, changed) {
				continue
			}
			quiet := ss.LastEventAt == nil || now.Sub(*ss.LastEventAt) > roomQuiet
			if ss.Status != StatusIdle || !quiet {
				continue
			}
			uctx, cancel := context.WithTimeout(ctx, 30*time.Second)
			out, err := o.Runner.Run(uctx, "systemctl", "restart", "aiagent-user-tmux@"+name+".service")
			cancel()
			if err != nil {
				log.Printf("room %s: %s", name, truncateRunes(string(out), 200))
			}
			break
		}
	}
}

// StartRooms keeps the rooms on the admin's model.
func (s *Service) StartRooms(ctx context.Context) {
	if s.OSUsers == nil {
		return
	}
	s.StartRoomCaptures(ctx)
	s.StartAdminList(ctx)
	go func() {
		t := time.NewTicker(time.Minute)
		defer t.Stop()
		s.applyRoomModel(ctx, time.Now())
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if s.Store.Enabled() {
					s.applyRoomModel(ctx, time.Now())
				}
			}
		}
	}()
}

// userCommands are the slash commands a user may type into their agent. The
// CLI completes and fuzzy-matches what is typed, so this is an allowlist: a
// list of forbidden ones would be walked around with "/mod".
var userCommands = map[string]bool{"/compact": true, "/clear": true, "/context": true, "/usage": true, "/rename": true}

// userText decides whether a user's message may reach their agent.
func userText(msg string) bool {
	msg = strings.TrimLeftFunc(msg, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r) || unicode.Is(unicode.Cf, r)
	})
	// "!" opens the CLI's shell mode, which runs a command with no agent in
	// between; "&" sends the work to a cloud session.
	if strings.HasPrefix(msg, "!") || strings.HasPrefix(msg, "&") {
		return false
	}
	if !strings.HasPrefix(msg, "/") {
		return true
	}
	word, _, _ := strings.Cut(msg, "\n")
	word = strings.Fields(word)[0]
	// a path such as /home/x/file is text, not a command
	return userCommands[word] || strings.HasPrefix(word, "/home/") || strings.HasPrefix(word, "/tmp/")
}

func (s *Service) handleRooms(c fiber.Ctx) error {
	if s.OSUsers == nil {
		return hidden(c)
	}
	model, _ := s.OSUsers.currentModel()
	m := fiber.Map{"policy": s.loadRoomPolicy(), "model": model, "lowModel": roomLowModel}
	if w, ok := s.weeklyUsage(c.Context()); ok {
		m["weekly"] = w
	}
	return c.JSON(okEnv(m))
}

func (s *Service) handleSetRooms(c fiber.Ctx) error {
	if s.OSUsers == nil {
		return hidden(c)
	}
	var p RoomPolicy
	if err := c.Bind().Body(&p); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(failEnv("AIAGENT_BAD_REQUEST", "ข้อมูลที่ส่งมาไม่ใช่ JSON ที่ถูกต้อง"))
	}
	return s.mutate(c, "aiagent.roomPolicy", []string{}, func() (any, error) {
		return fiber.Map{"policy": p}, s.saveRoomPolicy(p)
	})
}
