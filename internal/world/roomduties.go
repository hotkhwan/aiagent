// internal/world/roomduties.go
//
// A room's duty: what the person behind a user token does — builds pages
// (frontend, with a dev server behind the room's preview), works on services
// (backend, no preview), or tests on the shared UAT (tester, runs no app).
// The admin picks it when creating the token and can change it later.
//
// The duties live in one small file, /etc/aiagent/room-duties.conf by
// default, one "room=duty" per line. It is the only record: the token store
// does not repeat it, so the admin can also edit the file by hand, and
// room-share.py reads it to tell each room's agent its duty.
package world

import (
	"bufio"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// Duties a room can have.
const (
	DutyFrontend = "frontend"
	DutyBackend  = "backend"
	DutyTester   = "tester"
)

var validDuties = map[string]bool{DutyFrontend: true, DutyBackend: true, DutyTester: true}

// ValidDuty reports whether d is a duty; "" (not set) is valid too.
func ValidDuty(d string) bool { return d == "" || validDuties[d] }

// RoomDuties reads and writes the duties file.
type RoomDuties struct {
	Path string
	mu   sync.Mutex
}

// Load maps room to duty. A missing file is no duties; lines that are not
// "room=duty" with a known duty are skipped.
func (r *RoomDuties) Load() map[string]string {
	out := map[string]string{}
	if r == nil || r.Path == "" {
		return out
	}
	f, err := os.Open(r.Path)
	if err != nil {
		return out
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		room, duty, ok := strings.Cut(line, "=")
		room, duty = strings.TrimSpace(room), strings.TrimSpace(duty)
		if ok && osUserRE.MatchString(room) && validDuties[duty] {
			out[room] = duty
		}
	}
	return out
}

// Set records a room's duty; "" removes the room from the file.
func (r *RoomDuties) Set(room, duty string) error {
	if r == nil || r.Path == "" {
		return nil
	}
	if !osUserRE.MatchString(room) {
		return ErrBadRequest("ชื่อห้องไม่ถูกต้อง")
	}
	if !ValidDuty(duty) {
		return ErrBadRequest("บทบาทต้องเป็น frontend, backend หรือ tester")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	m := r.Load()
	if duty == "" {
		delete(m, room)
	} else {
		m[room] = duty
	}
	rooms := make([]string, 0, len(m))
	for k := range m {
		rooms = append(rooms, k)
	}
	sort.Strings(rooms)
	var b strings.Builder
	b.WriteString("# room=duty (frontend | backend | tester). Written by aiagent when the admin\n# sets a room's duty; room-share.py tells each room's agent its duty.\n")
	for _, k := range rooms {
		b.WriteString(k + "=" + m[k] + "\n")
	}
	if err := os.MkdirAll(filepath.Dir(r.Path), 0o755); err != nil {
		return err
	}
	tmp := r.Path + ".tmp"
	if err := os.WriteFile(tmp, []byte(b.String()), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, r.Path)
}
