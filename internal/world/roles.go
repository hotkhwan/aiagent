// internal/world/roles.go
//
// Two kinds of token. The admin token (made on the host with `aiagent enable`)
// may do everything. A user token, made by an admin on the web, may watch and
// talk to their own agents but not manage the machine, and has no PM or task
// board: userRoutes lists what it may call
// and everything else is refused, so a new route is closed to users until
// someone decides otherwise.
//
// A protected session is listed to users by name and state only. Its text is
// never sent to them and they cannot type into it. A session is protected when
// protected.json names it or when it runs in a workspace marked protected.
//
// This bounds the web surface only. An agent a user may command runs as this
// service's OS user and can read that user's files; real separation needs a
// separate OS user per team.
package world

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
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
	RoleAdmin = "admin"
	RoleUser  = "user"

	protectedFile = "protected.json"
	maxUserTokens = 100
)

// UserToken is one user's access. Only the hash of the token is kept.
type UserToken struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Hash      string    `json:"hash"`
	CreatedAt time.Time `json:"createdAt"`
	// OSUser is the account this token works in; empty when the service does
	// not manage OS users. An entry whose Hash is empty is being removed.
	OSUser string `json:"osUser,omitempty"`
}

// Identity is who a presented token belongs to.
type Identity struct {
	Role string `json:"role"`
	Name string `json:"name,omitempty"`
	// OSUser is the instance this token's requests are sent to.
	OSUser string `json:"osUser,omitempty"`
}

var (
	userNameRE = regexp.MustCompile(`^[\p{L}\p{M}\p{N} ._-]{1,40}$`)
	tokensMu   sync.Mutex
)

func hashMatches(storedHex string, presented [32]byte) bool {
	want, err := hex.DecodeString(storedHex)
	return err == nil && len(want) == len(presented) && subtle.ConstantTimeCompare(want, presented[:]) == 1
}

// Identify names the holder of a token; ok is false for an unknown token and
// whenever the feature is off.
func (s *Store) Identify(presented string) (Identity, bool) {
	if presented == "" {
		return Identity{}, false
	}
	st, err := s.Load()
	if err != nil || !st.Enabled || st.TokenHash == "" {
		return Identity{}, false
	}
	sum := sha256.Sum256([]byte(presented))
	if hashMatches(st.TokenHash, sum) {
		return Identity{Role: RoleAdmin}, true
	}
	for _, u := range st.Users {
		if hashMatches(u.Hash, sum) {
			return Identity{Role: RoleUser, Name: u.Name, OSUser: u.OSUser}, true
		}
	}
	return Identity{}, false
}

// AddUser mints a user token and returns it once.
func (s *Store) AddUser(name, osUser string) (UserToken, string, error) {
	name = strings.TrimSpace(name)
	if !userNameRE.MatchString(name) {
		return UserToken{}, "", ErrBadRequest("name must be 1-40 letters, digits, spaces, dots or dashes")
	}
	tokensMu.Lock()
	defer tokensMu.Unlock()
	st, err := s.Load()
	if err != nil || !st.Enabled {
		return UserToken{}, "", ErrConflict("the service is not enabled")
	}
	if len(st.Users) >= maxUserTokens {
		return UserToken{}, "", ErrConflict("too many user tokens; revoke one first")
	}
	for _, u := range st.Users {
		if strings.EqualFold(u.Name, name) {
			return UserToken{}, "", ErrConflict("that name already has a token; revoke it first")
		}
	}
	token, err := newToken()
	if err != nil {
		return UserToken{}, "", err
	}
	var id [6]byte
	if _, err := rand.Read(id[:]); err != nil {
		return UserToken{}, "", err
	}
	sum := sha256.Sum256([]byte(token))
	u := UserToken{ID: hex.EncodeToString(id[:]), Name: name, Hash: hex.EncodeToString(sum[:]), CreatedAt: time.Now().UTC(), OSUser: osUser}
	// a fresh slice: Load's result shares its array with the cache
	st.Users = append(append([]UserToken{}, st.Users...), u)
	return u, token, s.write(st)
}

// ReissueUser replaces a user's token and returns the new one once. The old
// one stops working at once; the room behind it is untouched. A token cannot
// be shown again, only replaced: only its hash is kept.
func (s *Store) ReissueUser(id string) (UserToken, string, error) {
	tokensMu.Lock()
	defer tokensMu.Unlock()
	st, err := s.Load()
	if err != nil || !st.Enabled {
		return UserToken{}, "", ErrConflict("the service is not enabled")
	}
	users := append([]UserToken{}, st.Users...)
	for i := range users {
		if users[i].ID != id {
			continue
		}
		if users[i].Hash == "" {
			return UserToken{}, "", ErrConflict("this user is being removed")
		}
		token, err := newToken()
		if err != nil {
			return UserToken{}, "", err
		}
		sum := sha256.Sum256([]byte(token))
		users[i].Hash = hex.EncodeToString(sum[:])
		st.Users = users
		return users[i], token, s.write(st)
	}
	return UserToken{}, "", ErrNotFound("no such token")
}

// RevokeUser removes a user token; it stops working at once.
func (s *Store) RevokeUser(id string) error {
	tokensMu.Lock()
	defer tokensMu.Unlock()
	st, err := s.Load()
	if err != nil {
		return err
	}
	kept := []UserToken{}
	for _, u := range st.Users {
		if u.ID != id {
			kept = append(kept, u)
		}
	}
	if len(kept) == len(st.Users) {
		return ErrNotFound("no such token")
	}
	st.Users = kept
	return s.write(st)
}

// BlockUser ends a token's access while its entry stays listed, so a removal
// that fails half-way can be retried.
func (s *Store) BlockUser(id string) (UserToken, error) {
	tokensMu.Lock()
	defer tokensMu.Unlock()
	st, err := s.Load()
	if err != nil {
		return UserToken{}, err
	}
	users := append([]UserToken{}, st.Users...)
	for i := range users {
		if users[i].ID == id {
			users[i].Hash = ""
			st.Users = users
			return users[i], s.write(st)
		}
	}
	return UserToken{}, ErrNotFound("no such token")
}

func identity(c fiber.Ctx) Identity {
	id, _ := c.Locals("identity").(Identity)
	return id
}

// subject is what the audit log records as the actor.
func subject(c fiber.Ctx) string {
	if id := identity(c); id.Role == RoleUser {
		return "aiagent-user:" + id.Name + "@" + clientAddr(c)
	}
	return "aiagent-token@" + clientAddr(c)
}

const apiPrefix = "/aiagent/api"

// userRoutes is everything a user token may call. The path is matched
// decoded and ids admit no percent signs, so neither an encoded nor a
// double-encoded id slips past the protected-session check below.
var userRoutes = map[string][]*regexp.Regexp{
	"GET": compileAll(
		`/me`, `/guides/user-(manual|slides)`, `/snapshot`, `/models`, `/reviews`,
		`/sessions/[A-Za-z0-9-]+`, `/sessions/[A-Za-z0-9-]+/(messages|suggest)`,
		`/uploads/[A-Za-z0-9.-]+`,
	),
	"POST": compileAll(
		`/messages`, `/uploads`,
		`/sessions/[A-Za-z0-9-]+/(message|stop|name|look|answer)`,
		`/reviews/[A-Za-z0-9._-]+/(view|compose|decision)`,
	),
}

func compileAll(ps ...string) []*regexp.Regexp {
	out := make([]*regexp.Regexp, 0, len(ps))
	for _, p := range ps {
		out = append(out, regexp.MustCompile("^"+p+"$"))
	}
	return out
}

var (
	sessionPathRE = regexp.MustCompile(`^/sessions/([A-Za-z0-9-]+)`)
	reviewPathRE  = regexp.MustCompile(`^/reviews/([A-Za-z0-9._-]+)/`)
)

// userMay decides a user's request before any handler runs.
func (s *Service) userMay(c fiber.Ctx) bool {
	path, ok := strings.CutPrefix(c.Path(), apiPrefix)
	if !ok {
		return false
	}
	allowed := false
	for _, re := range userRoutes[c.Method()] {
		if re.MatchString(path) {
			allowed = true
			break
		}
	}
	if !allowed {
		return false
	}
	if m := sessionPathRE.FindStringSubmatch(path); m != nil {
		return !s.protections(c.Context()).covers(m[1])
	}
	if m := reviewPathRE.FindStringSubmatch(path); m != nil {
		p := s.protections(c.Context())
		for _, r := range s.Collector.Reviews() {
			if r.ID == m[1] {
				return r.SessionID != "" && !p.covers(r.SessionID)
			}
		}
	}
	return true
}

// protections is the set of protected session ids at one moment.
type protections struct {
	all bool // the list could not be read: fail closed
	ids map[string]bool
}

// covers also matches a shortened id, which some lookups accept.
func (p protections) covers(id string) bool {
	if p.all {
		return true
	}
	id = strings.ToLower(id)
	if p.ids[id] {
		return true
	}
	for k := range p.ids {
		if len(id) >= 4 && strings.HasPrefix(k, id) {
			return true
		}
	}
	return false
}

var protectedMu sync.Mutex

type protectedList struct {
	Sessions []string `json:"sessions"`
}

func (s *Service) protections(ctx context.Context) protections {
	return s.protectionsFor(s.Collector.Snapshot(ctx).Sessions)
}

// protectionsFor reads protected.json and adds every session running in a
// protected workspace. Those are written back, so the conversation stays
// protected after the agent exits.
func (s *Service) protectionsFor(sessions []Session) protections {
	protectedMu.Lock()
	defer protectedMu.Unlock()
	path := filepath.Join(s.Store.dir, protectedFile)
	var list protectedList
	b, err := os.ReadFile(path)
	if err == nil {
		err = json.Unmarshal(b, &list)
	} else if errors.Is(err, os.ErrNotExist) {
		err = nil
	}
	if err != nil {
		return protections{all: true}
	}
	p := protections{ids: map[string]bool{}}
	for _, id := range list.Sessions {
		p.ids[strings.ToLower(id)] = true
	}
	added := false
	for _, ss := range sessions {
		id := strings.ToLower(ss.SessionID)
		if w, ok := s.Collector.lookupWorkspace(ss.DispatchVia); ok && w.protected && !p.ids[id] {
			p.ids[id] = true
			list.Sessions = append(list.Sessions, ss.SessionID)
			added = true
		}
	}
	if added {
		if b, err := json.MarshalIndent(list, "", "  "); err == nil {
			tmp := path + ".tmp"
			if os.WriteFile(tmp, append(b, '\n'), 0o600) == nil {
				_ = os.Rename(tmp, path)
			}
		}
	}
	return p
}

// forRole marks protected sessions and, for a user, strips everything about
// them but their existence and state.
func (s *Service) forRole(id Identity, snap Snapshot) Snapshot {
	p := s.protectionsFor(snap.Sessions)
	looks := s.loadLooks()
	out := make([]Session, 0, len(snap.Sessions))
	for _, ss := range snap.Sessions {
		ss.Look = looks[ss.SessionID]
		if p.covers(ss.SessionID) {
			ss.Protected = true
			if id.Role != RoleAdmin {
				const why = "session ของผู้ดูแล — ดูข้อความและสั่งงานได้เฉพาะ admin"
				ss = Session{
					SessionID: ss.SessionID, RuntimeType: ss.RuntimeType, ProviderID: ss.ProviderID,
					DisplayName: RoleAdmin, Look: ss.Look, Status: ss.Status, Live: ss.Live, LiveEvidence: ss.LiveEvidence,
					ActivityType: ActivityUnknown, StartedAt: ss.StartedAt, LastEventAt: ss.LastEventAt,
					Kind: ss.Kind, Protected: true, MessageBlocked: why, StopBlocked: why,
				}
			}
		}
		out = append(out, ss)
	}
	snap.Sessions = out
	if id.Role == RoleAdmin {
		return snap
	}
	pend := []Pending{}
	for _, x := range snap.Pending {
		if !p.covers(x.SessionID) {
			pend = append(pend, x)
		}
	}
	snap.Pending = pend
	ws := []Workspace{}
	for _, w := range snap.Workspaces {
		if e, ok := s.Collector.lookupWorkspace(w.Name); !ok || !e.protected {
			ws = append(ws, w)
		}
	}
	snap.Workspaces = ws
	return snap
}

// reviewsFor hides from a user the pages filed by a protected session, and
// those that name no session, whose origin is unknown.
func (s *Service) reviewsFor(c fiber.Ctx, rs []Review) []Review {
	if identity(c).Role == RoleAdmin {
		return rs
	}
	p := s.protections(c.Context())
	out := []Review{}
	for _, r := range rs {
		if r.SessionID != "" && !p.covers(r.SessionID) {
			out = append(out, r)
		}
	}
	return out
}

func (s *Service) handleMe(c fiber.Ctx) error { return c.JSON(okEnv(identity(c))) }

func (s *Service) handleTokens(c fiber.Ctx) error {
	st, err := s.Store.Load()
	if err != nil {
		return s.fail(c, err)
	}
	out := []fiber.Map{}
	for _, u := range st.Users {
		out = append(out, fiber.Map{"id": u.ID, "name": u.Name, "createdAt": u.CreatedAt, "osUser": u.OSUser, "removing": u.Hash == ""})
	}
	return c.JSON(okEnv(fiber.Map{"tokens": out, "osUsers": s.OSUsers != nil}))
}

func (s *Service) handleTokenCreate(c fiber.Ctx) error {
	var body struct {
		Name string `json:"name"`
	}
	if err := c.Bind().Body(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(failEnv("AIAGENT_BAD_REQUEST", "invalid JSON body"))
	}
	c.Set("Cache-Control", "no-store")
	return s.mutate(c, "aiagent.tokenCreate", []string{body.Name}, func() (any, error) {
		osUser := ""
		if s.OSUsers != nil {
			osUser = strings.TrimSpace(body.Name)
			if !osUserRE.MatchString(osUser) {
				return nil, ErrBadRequest("user name must be lower-case letters, digits or dashes, starting with a letter")
			}
		}
		u, token, err := s.Store.AddUser(body.Name, osUser)
		if err != nil {
			return nil, err
		}
		if s.OSUsers != nil {
			// context.Background: a browser that goes away must not leave a
			// half-made account
			if err := s.OSUsers.unit(context.Background(), "add", osUser); err != nil {
				_ = s.Store.RevokeUser(u.ID)
				return nil, err
			}
		}
		return fiber.Map{"id": u.ID, "name": u.Name, "token": token}, nil
	})
}

func (s *Service) handleTokenReissue(c fiber.Ctx) error {
	id := strings.Clone(c.Params("id"))
	c.Set("Cache-Control", "no-store")
	return s.mutate(c, "aiagent.tokenReissue", []string{id}, func() (any, error) {
		u, token, err := s.Store.ReissueUser(id)
		if err != nil {
			return nil, err
		}
		return fiber.Map{"id": u.ID, "name": u.Name, "token": token}, nil
	})
}

func (s *Service) handleTokenRevoke(c fiber.Ctx) error {
	id := strings.Clone(c.Params("id"))
	return s.mutate(c, "aiagent.tokenRevoke", []string{id}, func() (any, error) {
		if s.OSUsers != nil {
			u, err := s.Store.BlockUser(id)
			if err != nil {
				return nil, err
			}
			if u.OSUser != "" {
				if err := s.OSUsers.unit(context.Background(), "del", u.OSUser); err != nil {
					return nil, err
				}
			}
		}
		return fiber.Map{"id": id}, s.Store.RevokeUser(id)
	})
}
