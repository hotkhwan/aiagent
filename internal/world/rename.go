// internal/world/rename.go
//
// Renaming a Claude Code session from the web.
//
// The name shown everywhere is the one Claude Code itself keeps for the
// session. There is no local alias: renaming on the web types `/rename <name>`
// into the session through its normal channel (agcmd or relay), exactly as
// SetModel types `/model`. The CLI then owns the name, and the web reads it
// back, so the two cannot drift apart.
package world

import (
	"context"
	"strings"
	"unicode"
	"unicode/utf8"
)

// maxSessionNameRunes matches what Claude Code shows in its session list.
const maxSessionNameRunes = 60

// cleanSessionName returns the name as one line of plain text. Runs of
// whitespace, including newlines, collapse to one space, so a name can never
// submit the command early. It refuses empty names, control characters, and a
// leading '-' so the text cannot be read as a flag.
func cleanSessionName(s string) (string, error) {
	if !utf8.ValidString(s) {
		return "", ErrBadRequest("name must be valid UTF-8")
	}
	s = strings.TrimSpace(s)
	s = strings.Join(strings.Fields(s), " ")
	if s == "" {
		return "", ErrBadRequest("name is required")
	}
	if utf8.RuneCountInString(s) > maxSessionNameRunes {
		return "", ErrBadRequest("name is longer than 60 characters")
	}
	if strings.HasPrefix(s, "-") {
		return "", ErrBadRequest("name must not start with '-'")
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return "", ErrBadRequest("name must not contain control characters")
		}
	}
	return s, nil
}

// RenameSession types `/rename <name>` into a live Claude Code session and
// returns the name it was asked to use plus the route taken. The web keeps no
// copy of the name: the next snapshot shows whatever Claude Code reports.
func (c *Collector) RenameSession(ctx context.Context, id, name string) (string, string, error) {
	if strings.TrimSpace(id) == "" {
		return "", "", ErrBadRequest("session id is required")
	}
	clean, err := cleanSessionName(name)
	if err != nil {
		return "", "", err
	}
	snap := c.Snapshot(ctx)
	for _, s := range snap.Sessions {
		if s.SessionID == id && s.RuntimeType != "claudeCode" {
			return "", "", ErrConflict("renaming from the web is supported for Claude Code sessions only")
		}
	}
	how, err := c.SendToSession(ctx, id, "/rename "+clean)
	if err != nil {
		return "", "", err
	}
	return clean, how, nil
}
