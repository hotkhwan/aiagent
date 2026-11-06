// internal/world/names.go
//
// Operator-chosen names for sessions.
//
// A session's name comes from the agent CLI — Claude Code derives one from the
// first prompt, so you end up with "audit littledays repo" on something that
// has long since moved on. `claude agents` has no rename, so this is a local
// alias: it changes what this service calls the session, and nothing else. The
// CLI keeps its own name, and clearing the alias shows that name again.
package world

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unicode/utf8"
)

const (
	namesFile    = "names.json"
	maxNameRunes = 60
)

var namesMu sync.Mutex

// nameAliases maps sessionId → operator-chosen name.
type nameAliases map[string]string

func (s *Service) namesPath() string { return filepath.Join(s.Store.dir, namesFile) }

func (s *Service) loadNames() nameAliases {
	out := nameAliases{}
	b, err := os.ReadFile(s.namesPath())
	if err != nil {
		return out
	}
	_ = json.Unmarshal(b, &out)
	return out
}

// SetSessionName records (or, with an empty name, clears) an alias. It returns
// the name now in effect for that session.
//
// The id is not validated against live sessions on purpose: an alias set on a
// session that later comes back should still apply, and a stale entry costs a
// few bytes and shows up nowhere.
func (s *Service) SetSessionName(sessionID, name string) (string, error) {
	if strings.TrimSpace(sessionID) == "" {
		return "", ErrBadRequest("session id is required")
	}
	name = strings.TrimSpace(strings.Map(func(r rune) rune {
		// One line, no control characters: this lands in a heading.
		if r == '\n' || r == '\t' || r < 0x20 {
			return ' '
		}
		return r
	}, name))
	name = strings.Join(strings.Fields(name), " ")
	if utf8.RuneCountInString(name) > maxNameRunes {
		return "", ErrBadRequest("name is too long")
	}
	if !utf8.ValidString(name) {
		return "", ErrBadRequest("name must be valid UTF-8")
	}

	namesMu.Lock()
	defer namesMu.Unlock()
	names := s.loadNames()
	if name == "" {
		delete(names, sessionID)
	} else {
		names[sessionID] = name
	}
	if err := os.MkdirAll(s.Store.dir, 0o700); err != nil {
		return "", err
	}
	b, err := json.MarshalIndent(names, "", "  ")
	if err != nil {
		return "", err
	}
	tmp := s.namesPath() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, s.namesPath()); err != nil {
		return "", err
	}
	s.Collector.invalidate()
	return name, nil
}

// applyNames overwrites DisplayName where the operator has chosen one, and
// records the CLI's own name so the UI can offer to put it back.
func (s *Service) applyNames(sessions []Session) []Session {
	names := s.loadNames()
	if len(names) == 0 {
		return sessions
	}
	for i := range sessions {
		if alias, ok := names[sessions[i].SessionID]; ok && alias != "" {
			sessions[i].OriginalName = sessions[i].DisplayName
			sessions[i].DisplayName = alias
		}
	}
	return sessions
}
