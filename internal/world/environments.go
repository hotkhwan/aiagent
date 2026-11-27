// internal/world/environments.go
//
// Environments are the places the rooms' work runs against (netra-dev,
// netra-uat, netra-prod), each with a tier and the variables a program there
// needs (DATABASE_URL, API_KEY). Only the admin lists or changes them. A
// value is write-only from the web: it is saved and never read back; the list
// says only whether a variable has one.
//
// Owner decision: a room's agent may hold credentials for dev and UAT, never
// for prod; prod work goes through the admin, as merge requests do. So the
// values of dev and uat environments are delivered to the rooms whose duty
// builds or runs code (deliverTo), as one file per environment in the room's
// own home, and a prod value is never in any room's file. The delivery is
// done by `aiagent room-env-sync`, a root oneshot (roomenv.go), because this
// service's sandbox may not write into a room's home. See docs/ENVIRONMENTS.md.
//
// Metadata and values are kept apart, like the vault: environments.json and
// environments-secrets.json, both 0600 in the state directory.
package world

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/gofiber/fiber/v3"
)

const (
	envFile         = "environments.json"
	envSecrets      = "environments-secrets.json"
	maxEnvironments = 30
	maxEnvVars      = 60
	maxEnvValue     = 4096
	// roomEnvUnit is the root oneshot that writes the rooms' files.
	roomEnvUnit = "aiagent-room-env.service"
)

// Tiers an environment can have.
const (
	TierDev  = "dev"
	TierUAT  = "uat"
	TierProd = "prod"
)

var (
	envMu sync.Mutex
	// an environment's name is a file name in a room's home
	envNameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)
	envKeyRE  = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,63}$`)
	envTiers  = map[string]bool{TierDev: true, TierUAT: true, TierProd: true}
	// a variable that would change how the room's shell or tools behave, or
	// stand in for this service's own settings, is not a project variable
	envReserved = map[string]bool{"PATH": true, "HOME": true, "USER": true, "LOGNAME": true, "SHELL": true, "IFS": true, "ENV": true,
		"BASH_ENV": true, "PS1": true, "PS4": true, "PROMPT_COMMAND": true, "TMUX": true, "TMUX_TMPDIR": true, "TERM": true, "PWD": true}
	envReservedPrefix = []string{"LD_", "DYLD_", "AIAGENT_", "BASH_FUNC_", "CLAUDE_", "GIT_"}
)

// deliverTo reports whether a room of this duty gets an environment of this
// tier. Builders (frontend, backend, fullstack) and admin rooms get dev and
// uat; a tester runs no app and gets none; a room with no duty gets none.
// Prod is never delivered, whatever the duty.
func deliverTo(duty, tier string) bool {
	if tier != TierDev && tier != TierUAT {
		return false
	}
	switch duty {
	case DutyFrontend, DutyBackend, DutyFullstack, DutyAdmin:
		return true
	}
	return false
}

// Environment is what the web lists: never a value.
type Environment struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Tier      string    `json:"tier"`
	Note      string    `json:"note"`
	Vars      []EnvVar  `json:"vars"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// EnvVar is one variable's key and description, and whether it has a value.
type EnvVar struct {
	Key         string `json:"key"`
	Description string `json:"description"`
	Set         bool   `json:"set"`
}

// envSecretMap is environment id → key → value.
type envSecretMap map[string]map[string]string

func (s *Service) envPath(name string) string { return filepath.Join(s.Store.dir, name) }

func (s *Service) loadEnvs() ([]Environment, envSecretMap, error) {
	var list []Environment
	if err := readJSON(s.envPath(envFile), &list); err != nil {
		return nil, nil, err
	}
	sec := envSecretMap{}
	if err := readJSON(s.envPath(envSecrets), &sec); err != nil {
		return nil, nil, err
	}
	if sec == nil {
		sec = envSecretMap{}
	}
	if list == nil {
		list = []Environment{}
	}
	for i := range list {
		if list[i].Vars == nil {
			list[i].Vars = []EnvVar{}
		}
		for j := range list[i].Vars {
			list[i].Vars[j].Set = sec[list[i].ID][list[i].Vars[j].Key] != ""
		}
	}
	return list, sec, nil
}

// saveEnvs writes the values first: a variable listed as set always has one.
func (s *Service) saveEnvs(list []Environment, sec envSecretMap) error {
	if err := writeSecret(s.envPath(envSecrets), sec); err != nil {
		return err
	}
	return writeSecret(s.envPath(envFile), list)
}

// Environments returns the entries, with no values.
func (s *Service) Environments() ([]Environment, error) {
	envMu.Lock()
	defer envMu.Unlock()
	list, _, err := s.loadEnvs()
	return list, err
}

func envFields(name, tier, note string) error {
	switch n := utf8.RuneCountInString(note); {
	case !envNameRE.MatchString(name):
		return ErrBadRequest("ชื่อ environment: a-z 0-9 และขีดกลาง ยาว 1-40 ตัว เช่น netra-dev")
	case !envTiers[tier]:
		return ErrBadRequest("tier ต้องเป็น dev, uat หรือ prod")
	case n > 300 || strings.ContainsAny(note, "\r\n"):
		return ErrBadRequest("หมายเหตุยาวไม่เกิน 300 ตัวอักษร บรรทัดเดียว")
	}
	return nil
}

func envKeyOK(key string) error {
	if !envKeyRE.MatchString(key) {
		return ErrBadRequest("ชื่อตัวแปร: A-Z 0-9 และ _ ขึ้นต้นด้วยตัวอักษร ยาวไม่เกิน 64 ตัว เช่น DATABASE_URL")
	}
	bad := envReserved[key]
	for _, p := range envReservedPrefix {
		bad = bad || strings.HasPrefix(key, p)
	}
	if bad {
		return ErrBadRequest("ชื่อตัวแปรนี้สงวนไว้ให้ระบบ ใช้ชื่ออื่น")
	}
	return nil
}

// envValueOK: one line, printable, within the limit. The file is sourced by a
// shell, so it is single-quoted when written; a newline or a NUL would not
// survive that, and has no place in a credential.
func envValueOK(v string) error {
	if len(v) > maxEnvValue || !utf8.ValidString(v) || strings.ContainsFunc(v, func(r rune) bool { return r < 0x20 && r != '\t' || r == 0x7f }) {
		return ErrBadRequest("ค่าตัวแปรยาวไม่เกิน 4096 ไบต์ บรรทัดเดียว ไม่มีอักขระควบคุม")
	}
	return nil
}

// AddEnvironment saves a new environment with no variables.
func (s *Service) AddEnvironment(name, tier, note string) (Environment, error) {
	name, note = strings.TrimSpace(name), strings.TrimSpace(note)
	if err := envFields(name, tier, note); err != nil {
		return Environment{}, err
	}
	envMu.Lock()
	defer envMu.Unlock()
	list, sec, err := s.loadEnvs()
	if err != nil {
		return Environment{}, err
	}
	if len(list) >= maxEnvironments {
		return Environment{}, ErrConflict("environment เต็มแล้ว ให้ลบรายการก่อน")
	}
	for _, en := range list {
		if en.Name == name {
			return Environment{}, ErrConflict("ชื่อ environment นี้มีแล้ว")
		}
	}
	var b [6]byte
	_, _ = rand.Read(b[:])
	now := time.Now().UTC()
	en := Environment{ID: hex.EncodeToString(b[:]), Name: name, Tier: tier, Note: note, Vars: []EnvVar{}, CreatedAt: now, UpdatedAt: now}
	list = append(list, en)
	return en, s.saveEnvs(list, sec)
}

// EditEnvironment renames an environment or changes its tier or note. Its
// variables and values stay.
func (s *Service) EditEnvironment(id, name, tier, note string) (Environment, error) {
	name, note = strings.TrimSpace(name), strings.TrimSpace(note)
	if err := envFields(name, tier, note); err != nil {
		return Environment{}, err
	}
	envMu.Lock()
	defer envMu.Unlock()
	list, sec, err := s.loadEnvs()
	if err != nil {
		return Environment{}, err
	}
	at := -1
	for i, en := range list {
		if en.ID == id {
			at = i
		} else if en.Name == name {
			return Environment{}, ErrConflict("ชื่อ environment นี้มีแล้ว")
		}
	}
	if at < 0 {
		return Environment{}, ErrNotFound("ไม่พบ environment นี้")
	}
	list[at].Name, list[at].Tier, list[at].Note, list[at].UpdatedAt = name, tier, note, time.Now().UTC()
	return list[at], s.saveEnvs(list, sec)
}

// RemoveEnvironment deletes an environment and every value in it.
func (s *Service) RemoveEnvironment(id string) error {
	envMu.Lock()
	defer envMu.Unlock()
	list, sec, err := s.loadEnvs()
	if err != nil {
		return err
	}
	kept := []Environment{}
	for _, en := range list {
		if en.ID != id {
			kept = append(kept, en)
		}
	}
	if len(kept) == len(list) {
		return ErrNotFound("ไม่พบ environment นี้")
	}
	delete(sec, id)
	return s.saveEnvs(kept, sec)
}

// SetEnvVar adds a variable or changes its description; a non-empty value
// replaces the stored one, an empty value keeps it (or leaves it unset).
func (s *Service) SetEnvVar(id, key, description, value string) (Environment, error) {
	key, description = strings.TrimSpace(key), strings.TrimSpace(description)
	if err := envKeyOK(key); err != nil {
		return Environment{}, err
	}
	if utf8.RuneCountInString(description) > 200 || strings.ContainsAny(description, "\r\n") {
		return Environment{}, ErrBadRequest("คำอธิบายยาวไม่เกิน 200 ตัวอักษร บรรทัดเดียว")
	}
	if err := envValueOK(value); err != nil {
		return Environment{}, err
	}
	envMu.Lock()
	defer envMu.Unlock()
	list, sec, err := s.loadEnvs()
	if err != nil {
		return Environment{}, err
	}
	for i := range list {
		if list[i].ID != id {
			continue
		}
		en := &list[i]
		at := -1
		for j, v := range en.Vars {
			if v.Key == key {
				at = j
			}
		}
		if at < 0 {
			if len(en.Vars) >= maxEnvVars {
				return Environment{}, ErrConflict("ตัวแปรใน environment นี้เต็มแล้ว")
			}
			en.Vars = append(en.Vars, EnvVar{Key: key})
			at = len(en.Vars) - 1
		}
		en.Vars[at].Description = description
		if value != "" {
			if sec[id] == nil {
				sec[id] = map[string]string{}
			}
			sec[id][key] = value
			en.Vars[at].Set = true
		}
		en.UpdatedAt = time.Now().UTC()
		return *en, s.saveEnvs(list, sec)
	}
	return Environment{}, ErrNotFound("ไม่พบ environment นี้")
}

// RemoveEnvVar deletes a variable and its value.
func (s *Service) RemoveEnvVar(id, key string) error {
	envMu.Lock()
	defer envMu.Unlock()
	list, sec, err := s.loadEnvs()
	if err != nil {
		return err
	}
	for i := range list {
		if list[i].ID != id {
			continue
		}
		kept := []EnvVar{}
		for _, v := range list[i].Vars {
			if v.Key != key {
				kept = append(kept, v)
			}
		}
		if len(kept) == len(list[i].Vars) {
			return ErrNotFound("ไม่พบตัวแปรนี้")
		}
		list[i].Vars, list[i].UpdatedAt = kept, time.Now().UTC()
		delete(sec[id], key)
		if len(sec[id]) == 0 {
			delete(sec, id)
		}
		return s.saveEnvs(list, sec)
	}
	return ErrNotFound("ไม่พบ environment นี้")
}

// RoomEnvPlan is, for every room, the env files it should hold (file name →
// content). Every room is in it, those that get nothing with an empty map, so
// a sync removes what a room should no longer have.
func (s *Service) RoomEnvPlan() (map[string]map[string][]byte, error) {
	st, err := s.Store.Load()
	if err != nil {
		return nil, err
	}
	envMu.Lock()
	list, sec, err := s.loadEnvs()
	envMu.Unlock()
	if err != nil {
		return nil, err
	}
	var rooms []string
	for _, u := range st.Users {
		if u.OSUser != "" && osUserRE.MatchString(u.OSUser) {
			rooms = append(rooms, u.OSUser)
		}
	}
	duties := s.Duties.Load()
	// a room being removed keeps nothing
	for _, u := range st.Users {
		if u.Hash == "" {
			delete(duties, u.OSUser)
		}
	}
	return envPlan(list, sec, rooms, duties), nil
}

// envPlan is RoomEnvPlan without the files: the one place that decides what a
// room receives.
func envPlan(list []Environment, sec envSecretMap, rooms []string, duties map[string]string) map[string]map[string][]byte {
	out := map[string]map[string][]byte{}
	for _, room := range rooms {
		files := map[string][]byte{}
		for _, en := range list {
			if deliverTo(duties[room], en.Tier) && envNameRE.MatchString(en.Name) {
				files[en.Name+".env"] = envFileContent(en, sec[en.ID])
			}
		}
		out[room] = files
	}
	return out
}

// envFileContent is one environment as a file a shell can source
// (`set -a; . ~/.config/aiagent/env/netra-dev.env; set +a`). A variable with
// no value is left out.
func envFileContent(en Environment, values map[string]string) []byte {
	var b strings.Builder
	b.WriteString("# " + en.Name + " (" + en.Tier + "), from the admin's aiagent. Replaced on every sync; do not edit.\n")
	b.WriteString("# Never print, log, commit or paste these values.\n")
	for _, v := range en.Vars {
		val := values[v.Key]
		if val == "" || envKeyOK(v.Key) != nil || envValueOK(val) != nil {
			continue
		}
		b.WriteString(v.Key + "='" + strings.ReplaceAll(val, "'", `'\''`) + "'\n")
	}
	return []byte(b.String())
}

// RoomEnvListing is what room-share.py tells the rooms: each environment's
// name and tier, one "name=tier" a line. No variable and no value is in it.
func (s *Service) RoomEnvListing() ([]byte, error) {
	list, err := s.Environments()
	if err != nil {
		return nil, err
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	var b strings.Builder
	b.WriteString("# environment=tier, written by aiagent room-env-sync; names only, never values\n")
	for _, en := range list {
		if envNameRE.MatchString(en.Name) && envTiers[en.Tier] {
			b.WriteString(en.Name + "=" + en.Tier + "\n")
		}
	}
	return []byte(b.String()), nil
}

// kickRoomEnv asks the root oneshot to sync the rooms now; its timer would
// do it within a minute anyway, so a failure here only delays it.
func (s *Service) kickRoomEnv() {
	if s.OSUsers == nil || s.OSUsers.Runner == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, _ = s.OSUsers.Runner.Run(ctx, "systemctl", "start", "--no-block", roomEnvUnit)
}

func (s *Service) mountEnvironments(api fiber.Router) {
	api.Get("/environments", s.handleEnvironments)
	api.Post("/environments", s.handleEnvironmentAdd)
	api.Post("/environments/:id/edit", s.handleEnvironmentEdit)
	api.Post("/environments/:id/remove", s.handleEnvironmentRemove)
	api.Post("/environments/:id/vars", s.handleEnvVar)
	api.Post("/environments/:id/vars/:key/remove", s.handleEnvVarRemove)
}

// Admin only: userRoutes does not list these, so a user token is refused
// before a handler runs.
func (s *Service) handleEnvironments(c fiber.Ctx) error {
	list, err := s.Environments()
	if err != nil {
		return s.fail(c, err)
	}
	c.Set("Cache-Control", "no-store")
	return c.JSON(okEnv(fiber.Map{"environments": list, "osUsers": s.OSUsers != nil}))
}

type envBody struct {
	Name string `json:"name"`
	Tier string `json:"tier"`
	Note string `json:"note"`
}

// maxEnvBody is far above one variable (a 4096-byte value and its key and
// description); anything larger is refused before it is parsed.
const maxEnvBody = 16 << 10

// envBind reads a JSON body; false means the refusal is already sent.
func envBind(c fiber.Ctx, v any) (bool, error) {
	if len(c.Body()) > maxEnvBody {
		return false, c.Status(fiber.StatusRequestEntityTooLarge).JSON(failEnv("AIAGENT_TOO_LARGE", "ข้อมูลที่ส่งมายาวเกินไป"))
	}
	if err := c.Bind().Body(v); err != nil {
		return false, c.Status(fiber.StatusBadRequest).JSON(failEnv("AIAGENT_BAD_REQUEST", "ข้อมูลที่ส่งมาไม่ใช่ JSON ที่ถูกต้อง"))
	}
	return true, nil
}

// envMutate is mutate, then a sync of the rooms when the write succeeded.
func (s *Service) envMutate(c fiber.Ctx, rpc string, params []string, fn func() (any, error)) error {
	c.Set("Cache-Control", "no-store")
	ok := false
	err := s.mutate(c, rpc, params, func() (any, error) {
		d, err := fn()
		ok = err == nil
		return d, err
	})
	if ok {
		s.kickRoomEnv()
	}
	return err
}

func (s *Service) handleEnvironmentAdd(c fiber.Ctx) error {
	var body envBody
	if ok, err := envBind(c, &body); !ok {
		return err
	}
	return s.envMutate(c, "aiagent.environmentAdd", []string{body.Name, body.Tier}, func() (any, error) {
		en, err := s.AddEnvironment(body.Name, body.Tier, body.Note)
		return fiber.Map{"environment": en}, err
	})
}

func (s *Service) handleEnvironmentEdit(c fiber.Ctx) error {
	var body envBody
	if ok, err := envBind(c, &body); !ok {
		return err
	}
	id := strings.Clone(c.Params("id"))
	return s.envMutate(c, "aiagent.environmentEdit", []string{id, body.Name, body.Tier}, func() (any, error) {
		en, err := s.EditEnvironment(id, body.Name, body.Tier, body.Note)
		return fiber.Map{"environment": en}, err
	})
}

func (s *Service) handleEnvironmentRemove(c fiber.Ctx) error {
	id := strings.Clone(c.Params("id"))
	return s.envMutate(c, "aiagent.environmentRemove", []string{id}, func() (any, error) {
		return fiber.Map{"id": id}, s.RemoveEnvironment(id)
	})
}

func (s *Service) handleEnvVar(c fiber.Ctx) error {
	var body struct {
		Key         string `json:"key"`
		Description string `json:"description"`
		Value       string `json:"value"`
	}
	if ok, err := envBind(c, &body); !ok {
		return err
	}
	id := strings.Clone(c.Params("id"))
	// the value is not an audit parameter, and the answer never carries it
	return s.envMutate(c, "aiagent.environmentVar", []string{id, body.Key}, func() (any, error) {
		en, err := s.SetEnvVar(id, body.Key, body.Description, body.Value)
		return fiber.Map{"environment": en}, err
	})
}

func (s *Service) handleEnvVarRemove(c fiber.Ctx) error {
	id, key := strings.Clone(c.Params("id")), strings.Clone(c.Params("key"))
	return s.envMutate(c, "aiagent.environmentVarRemove", []string{id, key}, func() (any, error) {
		return fiber.Map{"id": id, "key": key}, s.RemoveEnvVar(id, key)
	})
}
