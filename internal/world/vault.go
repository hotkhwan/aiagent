// internal/world/vault.go
//
// The vault keeps the access tokens the admin gives this machine (GitLab,
// GitHub, others): each has a name and a note on what it is for. Only the
// admin sees the list. A value is shown once, when it is saved; the list never
// carries it. The values sit in a root-only file (0600), apart from the state
// file, and no room (an OS user of its own) can read it.
package world

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/gofiber/fiber/v3"
)

const (
	vaultFile    = "vault.json"
	vaultSecrets = "vault-secrets.json"
	maxVault     = 50
)

var (
	vaultMu sync.Mutex
	// a name is for people; it is never a path or an argument
	vaultNameRE = regexp.MustCompile(`^[\p{L}\p{M}\p{N}][\p{L}\p{M}\p{N} ._/-]{0,59}$`)
	vaultProv   = map[string]bool{"gitlab": true, "github": true, "other": true}
)

// VaultEntry is what the web lists: never the value.
type VaultEntry struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	Provider  string     `json:"provider"`
	Purpose   string     `json:"purpose"`
	CreatedAt time.Time  `json:"createdAt"`
	LastUsed  *time.Time `json:"lastUsed,omitempty"`
}

type vaultSecret struct {
	Value string `json:"value"`
}

func (s *Service) vaultDir() string { return s.Store.dir }

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

// writeSecret writes a file that only root can read, through a temp file so a
// crash never leaves half of it.
func writeSecret(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Vault returns the entries (no values) and whether a value exists for each.
func (s *Service) Vault() ([]VaultEntry, error) {
	vaultMu.Lock()
	defer vaultMu.Unlock()
	var list []VaultEntry
	if err := readJSON(filepath.Join(s.vaultDir(), vaultFile), &list); err != nil {
		return nil, err
	}
	if list == nil {
		list = []VaultEntry{}
	}
	return list, nil
}

// VaultValue returns the value of a named entry, for a service that needs it
// (netra-mr, CI). It records when the entry was used. A room never calls this.
func (s *Service) VaultValue(name string) (string, error) {
	vaultMu.Lock()
	defer vaultMu.Unlock()
	var list []VaultEntry
	if err := readJSON(filepath.Join(s.vaultDir(), vaultFile), &list); err != nil {
		return "", err
	}
	var secrets map[string]vaultSecret
	if err := readJSON(filepath.Join(s.vaultDir(), vaultSecrets), &secrets); err != nil {
		return "", err
	}
	for i := range list {
		if list[i].Name != name {
			continue
		}
		now := time.Now().UTC()
		list[i].LastUsed = &now
		_ = writeSecret(filepath.Join(s.vaultDir(), vaultFile), list)
		return secrets[list[i].ID].Value, nil
	}
	return "", ErrNotFound("no vault entry with that name")
}

// AddVault saves a token under a name and returns the entry.
func (s *Service) AddVault(name, provider, purpose, value string) (VaultEntry, error) {
	name = strings.TrimSpace(name)
	purpose = strings.TrimSpace(purpose)
	value = strings.TrimSpace(value)
	if err := vaultFields(name, provider, purpose); err != nil {
		return VaultEntry{}, err
	}
	if err := vaultToken(value); err != nil {
		return VaultEntry{}, err
	}
	vaultMu.Lock()
	defer vaultMu.Unlock()
	var list []VaultEntry
	if err := readJSON(filepath.Join(s.vaultDir(), vaultFile), &list); err != nil {
		return VaultEntry{}, err
	}
	if len(list) >= maxVault {
		return VaultEntry{}, ErrConflict("the vault is full; remove an entry first")
	}
	for _, e := range list {
		if strings.EqualFold(e.Name, name) {
			return VaultEntry{}, ErrConflict("ชื่อนี้มีแล้ว ใช้ปุ่มแก้ไขที่รายการเดิม")
		}
	}
	var sb [6]byte
	_, _ = rand.Read(sb[:])
	e := VaultEntry{ID: hex.EncodeToString(sb[:]), Name: name, Provider: provider, Purpose: purpose, CreatedAt: time.Now().UTC()}
	var secrets map[string]vaultSecret
	if err := readJSON(filepath.Join(s.vaultDir(), vaultSecrets), &secrets); err != nil {
		return VaultEntry{}, err
	}
	if secrets == nil {
		secrets = map[string]vaultSecret{}
	}
	secrets[e.ID] = vaultSecret{Value: value}
	if err := writeSecret(filepath.Join(s.vaultDir(), vaultSecrets), secrets); err != nil {
		return VaultEntry{}, err
	}
	list = append(list, e)
	return e, writeSecret(filepath.Join(s.vaultDir(), vaultFile), list)
}

func vaultFields(name, provider, purpose string) error {
	switch n := len([]rune(purpose)); {
	case !vaultNameRE.MatchString(name):
		return ErrBadRequest("ชื่อ: ตัวอักษร ตัวเลข เว้นวรรค . _ / - ยาว 1-60 ตัว")
	case !vaultProv[provider]:
		return ErrBadRequest("provider must be gitlab, github or other")
	case n < 3 || n > 300 || strings.ContainsAny(purpose, "\r\n"):
		return ErrBadRequest("บอกว่าใช้กับอะไร ยาว 3-300 ตัวอักษร")
	}
	return nil
}

func vaultToken(v string) error {
	if len(v) < 16 || len(v) > 400 || strings.ContainsAny(v, " \t\r\n\"'") {
		return ErrBadRequest("ค่า token ต้องยาว 16-400 ตัว ไม่มีเว้นวรรคหรือเครื่องหมายคำพูด")
	}
	return nil
}

// EditVault changes an entry. An empty value keeps the stored token.
func (s *Service) EditVault(id, name, provider, purpose, value string) (VaultEntry, error) {
	name, purpose, value = strings.TrimSpace(name), strings.TrimSpace(purpose), strings.TrimSpace(value)
	if err := vaultFields(name, provider, purpose); err != nil {
		return VaultEntry{}, err
	}
	if value != "" {
		if err := vaultToken(value); err != nil {
			return VaultEntry{}, err
		}
	}
	vaultMu.Lock()
	defer vaultMu.Unlock()
	var list []VaultEntry
	if err := readJSON(filepath.Join(s.vaultDir(), vaultFile), &list); err != nil {
		return VaultEntry{}, err
	}
	at := -1
	for i, e := range list {
		if e.ID == id {
			at = i
		} else if strings.EqualFold(e.Name, name) {
			return VaultEntry{}, ErrConflict("ชื่อนี้มีแล้ว ใช้ปุ่มแก้ไขที่รายการเดิม")
		}
	}
	if at < 0 {
		return VaultEntry{}, ErrNotFound("no such vault entry")
	}
	if value != "" {
		secrets := map[string]vaultSecret{}
		if err := readJSON(filepath.Join(s.vaultDir(), vaultSecrets), &secrets); err != nil {
			return VaultEntry{}, err
		}
		secrets[id] = vaultSecret{Value: value}
		if err := writeSecret(filepath.Join(s.vaultDir(), vaultSecrets), secrets); err != nil {
			return VaultEntry{}, err
		}
	}
	list[at].Name, list[at].Provider, list[at].Purpose = name, provider, purpose
	return list[at], writeSecret(filepath.Join(s.vaultDir(), vaultFile), list)
}

// RemoveVault deletes an entry and its value.
func (s *Service) RemoveVault(id string) error {
	vaultMu.Lock()
	defer vaultMu.Unlock()
	var list []VaultEntry
	if err := readJSON(filepath.Join(s.vaultDir(), vaultFile), &list); err != nil {
		return err
	}
	kept := []VaultEntry{}
	found := false
	for _, e := range list {
		if e.ID == id {
			found = true
			continue
		}
		kept = append(kept, e)
	}
	if !found {
		return ErrNotFound("no such vault entry")
	}
	var secrets map[string]vaultSecret
	if err := readJSON(filepath.Join(s.vaultDir(), vaultSecrets), &secrets); err != nil {
		return err
	}
	delete(secrets, id)
	if err := writeSecret(filepath.Join(s.vaultDir(), vaultSecrets), secrets); err != nil {
		return err
	}
	return writeSecret(filepath.Join(s.vaultDir(), vaultFile), kept)
}

// Admin only: userRoutes does not list these, so a user token is refused
// before a handler runs.
func (s *Service) handleVault(c fiber.Ctx) error {
	list, err := s.Vault()
	if err != nil {
		return s.fail(c, err)
	}
	return c.JSON(okEnv(fiber.Map{"entries": list}))
}

func (s *Service) handleVaultAdd(c fiber.Ctx) error {
	var body struct {
		Name     string `json:"name"`
		Provider string `json:"provider"`
		Purpose  string `json:"purpose"`
		Value    string `json:"value"`
	}
	if err := c.Bind().Body(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(failEnv("AIAGENT_BAD_REQUEST", "invalid JSON body"))
	}
	c.Set("Cache-Control", "no-store")
	// the value is not an audit parameter: only the name and the provider are
	return s.mutate(c, "aiagent.vaultAdd", []string{body.Name, body.Provider}, func() (any, error) {
		e, err := s.AddVault(body.Name, body.Provider, body.Purpose, body.Value)
		return fiber.Map{"entry": e}, err
	})
}

func (s *Service) handleVaultRemove(c fiber.Ctx) error {
	id := strings.Clone(c.Params("id"))
	return s.mutate(c, "aiagent.vaultRemove", []string{id}, func() (any, error) {
		return fiber.Map{"id": id}, s.RemoveVault(id)
	})
}

func (s *Service) handleVaultEdit(c fiber.Ctx) error {
	var body struct {
		Name     string `json:"name"`
		Provider string `json:"provider"`
		Purpose  string `json:"purpose"`
		Value    string `json:"value"`
	}
	if err := c.Bind().Body(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(failEnv("AIAGENT_BAD_REQUEST", "invalid JSON body"))
	}
	id := strings.Clone(c.Params("id"))
	c.Set("Cache-Control", "no-store")
	return s.mutate(c, "aiagent.vaultEdit", []string{id, body.Name, body.Provider}, func() (any, error) {
		e, err := s.EditVault(id, body.Name, body.Provider, body.Purpose, body.Value)
		return fiber.Map{"entry": e}, err
	})
}
