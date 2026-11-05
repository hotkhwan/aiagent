// internal/aiagent/state.go
//
// AI Agent World is off by default: switched on with `aiagent enable` and
// off again with `aiagent disable`, on this host only. The switch is a small 0600 state file
// that the running webinit re-reads on change, so toggling needs no restart.
//
// Enabling mints a dedicated access token. Only its SHA-256 is stored; the
// plaintext is shown exactly once to the operator who enabled the feature.
// Disabling removes the hash, so a leaked token dies with the switch.
package world

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const stateFileName = "state.json"

// State is the persisted switch. TokenHash is hex(sha256(token)); the token
// itself is never written anywhere.
type State struct {
	Enabled   bool      `json:"enabled"`
	TokenHash string    `json:"tokenHash,omitempty"`
	ChangedAt time.Time `json:"changedAt"`
	ChangedBy string    `json:"changedBy,omitempty"`
}

// Store reads/writes the switch file. Reads are cached by mtime+size so the
// HTTP gate can consult it on every request cheaply.
type Store struct {
	dir string

	mu      sync.Mutex
	cached  State
	modTime time.Time
	size    int64
	loaded  bool
}

// NewStore returns a store rooted at dir (DefaultStateDir() when empty).
func NewStore(dir string) *Store {
	if dir == "" {
		dir = DefaultStateDir()
	}
	return &Store{dir: dir}
}

func (s *Store) path() string { return filepath.Join(s.dir, stateFileName) }

// Load returns the current switch state. A missing file means disabled.
func (s *Store) Load() (State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fi, err := os.Stat(s.path())
	if errors.Is(err, os.ErrNotExist) {
		s.cached, s.loaded = State{}, true
		s.modTime, s.size = time.Time{}, 0
		return State{}, nil
	}
	if err != nil {
		return State{}, err
	}
	if s.loaded && fi.ModTime().Equal(s.modTime) && fi.Size() == s.size {
		return s.cached, nil
	}
	b, err := os.ReadFile(s.path())
	if err != nil {
		return State{}, err
	}
	var st State
	if err := json.Unmarshal(b, &st); err != nil {
		// A corrupt switch file fails CLOSED (feature off), never open.
		return State{}, fmt.Errorf("aiagent state: %w", err)
	}
	s.cached, s.loaded, s.modTime, s.size = st, true, fi.ModTime(), fi.Size()
	return st, nil
}

// Enabled reports whether the feature is on; any read error counts as off.
func (s *Store) Enabled() bool {
	st, err := s.Load()
	return err == nil && st.Enabled && st.TokenHash != ""
}

// VerifyToken checks a presented token in constant time. It is false whenever
// the feature is off.
func (s *Store) VerifyToken(presented string) bool {
	if presented == "" {
		return false
	}
	st, err := s.Load()
	if err != nil || !st.Enabled || st.TokenHash == "" {
		return false
	}
	want, err := hex.DecodeString(st.TokenHash)
	if err != nil {
		return false
	}
	got := sha256.Sum256([]byte(presented))
	return subtle.ConstantTimeCompare(want, got[:]) == 1
}

// Enable switches the feature on and mints a fresh token (returned once).
// Re-enabling an already-enabled box rotates the token.
func (s *Store) Enable(by string) (string, error) {
	token, err := newToken()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(token))
	st := State{Enabled: true, TokenHash: hex.EncodeToString(sum[:]), ChangedAt: time.Now().UTC(), ChangedBy: by}
	if err := s.write(st); err != nil {
		return "", err
	}
	return token, nil
}

// Disable switches the feature off and forgets the token hash.
func (s *Store) Disable(by string) error {
	return s.write(State{Enabled: false, ChangedAt: time.Now().UTC(), ChangedBy: by})
}

func (s *Store) write(st State) error {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return fmt.Errorf("aiagent state dir: %w", err)
	}
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.dir, ".state-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), s.path()); err != nil {
		return err
	}
	s.mu.Lock()
	s.loaded = false
	s.mu.Unlock()
	return nil
}

func newToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("aiagent token: %w", err)
	}
	return "kaw_" + base64.RawURLEncoding.EncodeToString(b), nil
}
