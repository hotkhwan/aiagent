// internal/world/looks.go
//
// The character chosen for a session. Kept here rather than in one browser, so
// the admin and the users see the same face on the same session.
package world

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sync"

	"github.com/gofiber/fiber/v3"
)

const (
	looksFile = "looks.json"
	maxLooks  = 2000
)

var (
	looksMu sync.Mutex
	lookRE  = regexp.MustCompile(`^[a-z0-9-]{1,24}$`)
)

func (s *Service) loadLooks() map[string]string {
	out := map[string]string{}
	if b, err := os.ReadFile(filepath.Join(s.Store.dir, looksFile)); err == nil {
		_ = json.Unmarshal(b, &out)
	}
	if out == nil { // the file held "null"
		out = map[string]string{}
	}
	return out
}

func (s *Service) setLook(id, look string) error {
	looksMu.Lock()
	defer looksMu.Unlock()
	path := filepath.Join(s.Store.dir, looksFile)
	m := map[string]string{}
	b, err := os.ReadFile(path)
	if err == nil {
		err = json.Unmarshal(b, &m)
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if m == nil {
		m = map[string]string{}
	}
	if look == "" {
		delete(m, id)
	} else if _, had := m[id]; !had && len(m) >= maxLooks {
		return ErrConflict("too many saved characters")
	} else {
		m[id] = look
	}
	if b, err = json.MarshalIndent(m, "", "  "); err != nil {
		return err
	}
	if err := os.WriteFile(path+".tmp", append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(path+".tmp", path)
}

func (s *Service) handleSessionLook(c fiber.Ctx) error {
	id := c.Params("id")
	var body struct {
		Look string `json:"look"`
	}
	if err := c.Bind().Body(&body); err != nil || (body.Look != "" && !lookRE.MatchString(body.Look)) {
		return s.fail(c, ErrBadRequest(`expected {"look": "<character>"}`))
	}
	known := false
	for _, ss := range s.Collector.Snapshot(c.Context()).Sessions {
		known = known || ss.SessionID == id
	}
	if !known {
		return s.fail(c, ErrNotFound("no session with that id"))
	}
	return s.mutate(c, "aiagent.sessionLook", []string{id}, func() (any, error) {
		return fiber.Map{"sessionId": id, "look": body.Look}, s.setLook(id, body.Look)
	})
}
