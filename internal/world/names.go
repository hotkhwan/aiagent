// internal/world/names.go
//
// Operator-chosen names for sessions.
//
// A session's name comes from the agent CLI — Claude Code derives one from the
// first prompt, so you end up with "audit littledays repo" on something that
// has long since moved on.
//
// Renaming from the web asks the CLI to rename itself first, by typing
// /rename into the session (Collector.RenameInCLI). That is the only outcome
// where the two agree, and when it works no alias is stored at all.
//
// What is here is the fallback, for a session the web cannot type into: a
// local alias that changes what this service calls the session and nothing
// else. It records the CLI name it was chosen over and steps aside the moment
// that changes, because a later `/rename` in the CLI is a newer statement of
// what the session is called, and having the web silently ignore it is worse
// than losing an alias nobody is attached to. Clearing the alias by hand also
// works, and shows the CLI name again.
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

// nameAlias is what the operator typed, plus the CLI name it replaced.
type nameAlias struct {
	Alias string `json:"alias"`
	// Over is the CLI's own name when the alias was set. Empty means the
	// entry predates this field, and such an alias keeps overriding — there
	// is nothing to compare against, and dropping it would throw away a name
	// the operator chose on no evidence at all.
	Over string `json:"over,omitempty"`
}

// nameAliases maps sessionId → alias.
type nameAliases map[string]nameAlias

// UnmarshalJSON reads both shapes: the current object, and the bare string
// this file used to hold.
func (n *nameAliases) UnmarshalJSON(b []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	out := nameAliases{}
	for k, v := range raw {
		var obj nameAlias
		if err := json.Unmarshal(v, &obj); err == nil && obj.Alias != "" {
			out[k] = obj
			continue
		}
		var str string
		if err := json.Unmarshal(v, &str); err == nil && str != "" {
			out[k] = nameAlias{Alias: str}
		}
	}
	*n = out
	return nil
}

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

// cleanSessionName makes a name safe to put in a heading and to type after
// "/rename " — one line, no control characters, so it cannot carry a second
// command.
func cleanSessionName(name string) (string, error) {
	name = strings.TrimSpace(strings.Map(func(r rune) rune {
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
	return name, nil
}

// SetSessionName records (or, with an empty name, clears) an alias. `over` is
// the CLI's own name for that session right now; it is what lets a later CLI
// rename take the name back. It returns the name now in effect.
//
// The id is not validated against live sessions on purpose: an alias set on a
// session that later comes back should still apply, and a stale entry costs a
// few bytes and shows up nowhere.
func (s *Service) SetSessionName(sessionID, name, over string) (string, error) {
	if strings.TrimSpace(sessionID) == "" {
		return "", ErrBadRequest("session id is required")
	}
	name, err := cleanSessionName(name)
	if err != nil {
		return "", err
	}

	namesMu.Lock()
	defer namesMu.Unlock()
	names := s.loadNames()
	if name == "" {
		delete(names, sessionID)
	} else {
		names[sessionID] = nameAlias{Alias: name, Over: over}
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

// cliName is the CLI's own name for a session, ignoring any alias.
func (s *Service) cliName(sessions []Session, id string) string {
	for i := range sessions {
		if sessions[i].SessionID == id {
			return sessions[i].DisplayName
		}
	}
	return ""
}

// applyNames overwrites DisplayName where the operator has chosen one, and
// records the CLI's own name so the UI can offer to put it back.
//
// An alias whose Over no longer matches the CLI name is skipped: the session
// has been renamed in the CLI since, and that is the more recent intent.
func (s *Service) applyNames(sessions []Session) []Session {
	names := s.loadNames()
	if len(names) == 0 {
		return sessions
	}
	for i := range sessions {
		a, ok := names[sessions[i].SessionID]
		if !ok || a.Alias == "" {
			continue
		}
		if a.Over != "" && a.Over != sessions[i].DisplayName {
			continue // renamed in the CLI since; the CLI name wins
		}
		sessions[i].OriginalName = sessions[i].DisplayName
		sessions[i].DisplayName = a.Alias
	}
	return sessions
}
