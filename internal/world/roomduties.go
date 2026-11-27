// internal/world/roomduties.go
//
// A room's duty: what the person behind a user token does.
//
//   - frontend: builds pages; its dev server runs behind dev/<room>.
//   - backend: works on services; it also has a preview, its own dev server
//     behind dev/<room> showing Swagger/OpenAPI or a small page of its own,
//     plus tests.
//   - fullstack: frontend and backend and the services they use, on dev/UAT.
//   - tester: runs no app; tests on the shared UAT.
//   - admin: the room's sessions are on the admin list in protected.json,
//     which the host's guard reads (see adminlist.go).
//
// Whatever the duty, merge requests go through the admin.
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
	DutyFrontend  = "frontend"
	DutyBackend   = "backend"
	DutyFullstack = "fullstack"
	DutyTester    = "tester"
	DutyAdmin     = "admin"
)

var validDuties = map[string]bool{DutyFrontend: true, DutyBackend: true, DutyFullstack: true, DutyTester: true, DutyAdmin: true}

// dutyErr is the refusal for a duty that is not one of validDuties.
const dutyErr = "บทบาทต้องเป็น frontend (dev server หลัง dev/<ห้อง>), backend (มีพรีวิวของตัวเองหลัง dev/<ห้อง> แสดง Swagger/OpenAPI หรือหน้าเล็ก ๆ พร้อมเทสต์), fullstack (frontend + backend + service ที่ใช้ บน dev/UAT), tester (ไม่รันแอป ทดสอบบน UAT) หรือ admin (อยู่ในรายชื่อ admin ของ guard) — ทุกบทบาทส่ง merge request ผ่าน admin"

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
		return ErrBadRequest(dutyErr)
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
	b.WriteString("# room=duty (frontend | backend | fullstack | tester | admin). Written by aiagent when the admin\n# sets a room's duty; room-share.py tells each room's agent its duty.\n")
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
