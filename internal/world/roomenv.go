// internal/world/roomenv.go
//
// Delivery of environment files to the rooms (environments.go decides what).
//
// `aiagent room-env-sync` runs as root from aiagent-room-env.service, started
// by the admin service after every change and by a timer every minute (which
// also picks up a duty changed by hand in room-duties.conf). Root never writes
// into a room's home itself: a home belongs to its room, which could plant a
// symlink there. For each room it starts `aiagent room-env-write` AS THAT
// ROOM'S USER, with the room's files on stdin; that process writes them to
// ~/.config/aiagent/env/ and removes any *.env there that is no longer meant
// for the room. Whatever the room planted, the writer can only touch what the
// room itself could.
package world

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// RoomEnvSubdir is where a room's env files live, under its home.
const RoomEnvSubdir = ".config/aiagent/env"

var envFileRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}\.env$`)

// WriteRoomEnvDir makes dir hold exactly these env files: each written 0600
// through a temp file, the folder 0700, and every other *.env in it removed.
// Files that are not *.env are the room's own and are left alone.
func WriteRoomEnvDir(dir string, files map[string][]byte) error {
	for name := range files {
		if !envFileRE.MatchString(name) {
			return fmt.Errorf("not an env file name: %q", name)
		}
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return err
	}
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	var errs []error
	for _, n := range names {
		errs = append(errs, writeEnvFile(filepath.Join(dir, n), files[n]))
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		return errors.Join(append(errs, err)...)
	}
	for _, e := range ents {
		n := e.Name()
		_, keep := files[n]
		stale := strings.HasSuffix(n, ".env") && !keep
		half := strings.HasSuffix(n, ".env.tmp")
		if stale || half {
			errs = append(errs, os.Remove(filepath.Join(dir, n)))
		}
	}
	return errors.Join(errs...)
}

func writeEnvFile(path string, b []byte) error {
	if old, err := os.ReadFile(path); err == nil && string(old) == string(b) {
		return os.Chmod(path, 0o600)
	}
	tmp := path + ".tmp"
	_ = os.Remove(tmp)
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

// RoomEnvDeliver writes one room's files; the real one runs the writer as the
// room's user (cmd/aiagent/roomenv.go).
type RoomEnvDeliver func(room string, files map[string][]byte) error

// SyncRoomEnv delivers the plan to every room, and returns one error per room
// that failed; a room that fails does not stop the others.
func (s *Service) SyncRoomEnv(deliver RoomEnvDeliver) []error {
	plan, err := s.RoomEnvPlan()
	if err != nil {
		return []error{err}
	}
	rooms := make([]string, 0, len(plan))
	for r := range plan {
		rooms = append(rooms, r)
	}
	sort.Strings(rooms)
	var errs []error
	for _, r := range rooms {
		if err := deliver(r, plan[r]); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", r, err))
		}
	}
	return errs
}
