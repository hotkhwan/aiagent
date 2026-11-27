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
	// UserID is a user token's stable id (UserToken.ID), kept across a
	// reissue; empty for the admin. Never sent: it names the token in
	// /tokens/:id routes only an admin may call.
	UserID string `json:"-"`
	// DeviceID is set when the token is a paired phone's (devices.go).
	DeviceID string `json:"-"`
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
	return s.identifyIn(st, sha256.Sum256([]byte(presented)))
}

// IdentifyHash names the holder of a token known only by its SHA-256, as a
// registered phone is (push.go). A revoked or rotated token matches nothing.
func (s *Store) IdentifyHash(sum [32]byte) (Identity, bool) {
	st, err := s.Load()
	if err != nil || !st.Enabled || st.TokenHash == "" {
		return Identity{}, false
	}
	return s.identifyIn(st, sum)
}

func (s *Store) identifyIn(st State, sum [32]byte) (Identity, bool) {
	if hashMatches(st.TokenHash, sum) {
		return Identity{Role: RoleAdmin}, true
	}
	for _, u := range st.Users {
		if hashMatches(u.Hash, sum) {
			return Identity{Role: RoleUser, Name: u.Name, OSUser: u.OSUser, UserID: u.ID}, true
		}
	}
	return identifyDevice(st, sum)
}

// AddUser mints a user token and returns it once.
func (s *Store) AddUser(name, osUser string) (UserToken, string, error) {
	name = strings.TrimSpace(name)
	if !userNameRE.MatchString(name) {
		return UserToken{}, "", ErrBadRequest("ชื่อ: ตัวอักษร ตัวเลข เว้นวรรค จุด หรือขีด ยาว 1-40 ตัว")
	}
	tokensMu.Lock()
	defer tokensMu.Unlock()
	st, err := s.Load()
	if err != nil || !st.Enabled {
		return UserToken{}, "", ErrConflict("บริการยังไม่เปิดใช้งาน")
	}
	if len(st.Users) >= maxUserTokens {
		return UserToken{}, "", ErrConflict("token ของผู้ใช้เต็มแล้ว เพิกถอนบางอันก่อน")
	}
	for _, u := range st.Users {
		if strings.EqualFold(u.Name, name) {
			return UserToken{}, "", ErrConflict("ชื่อนี้มี token แล้ว ใช้ปุ่มออก token ใหม่ หรือเพิกถอนก่อน")
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
		return UserToken{}, "", ErrConflict("บริการยังไม่เปิดใช้งาน")
	}
	users := append([]UserToken{}, st.Users...)
	for i := range users {
		if users[i].ID != id {
			continue
		}
		if users[i].Hash == "" {
			return UserToken{}, "", ErrConflict("กำลังลบผู้ใช้นี้อยู่")
		}
		token, err := newToken()
		if err != nil {
			return UserToken{}, "", err
		}
		sum := sha256.Sum256([]byte(token))
		users[i].Hash = hex.EncodeToString(sum[:])
		st.Users = users
		st.Devices = devicesWithout(st.Devices, id)
		return users[i], token, s.write(st)
	}
	return UserToken{}, "", ErrNotFound("ไม่พบ token นี้")
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
		return ErrNotFound("ไม่พบ token นี้")
	}
	st.Users = kept
	st.Devices = devicesWithout(st.Devices, id)
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
	return UserToken{}, ErrNotFound("ไม่พบ token นี้")
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
		`/faces`, `/faces/account`, `/faces/sessions/[A-Za-z0-9-]+`,
		`/connect/devices`,
	),
	// photo faces (faces.go): each token only ever reaches its own
	"PUT":    compileAll(`/faces/(account|settings)`, `/faces/sessions/[A-Za-z0-9-]+`),
	"DELETE": compileAll(`/faces/account`, `/faces/sessions/[A-Za-z0-9-]+`),
	"POST": compileAll(
		`/messages`, `/uploads`, `/preview/ticket`, `/push/devices`, `/push/devices/remove`, `/connect/pair`, `/connect/pair/cancel`, `/connect/devices/[0-9a-f]{12}/revoke`,
		// signing a browser in with this token's own scope (loginqr.go)
		`/login-qr/[A-Za-z0-9_-]{22}/(info|approve)`,
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
	// adminRoom maps a session of a room set to admin to that room. Those
	// are on the guard's list but are not the main service's: their own room
	// keeps them, the admin sees them, other rooms do not (teammates).
	adminRoom map[string]string
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

// protectedList is protected.json. Sessions is the list the host's guard
// reads as its admins, and what users are kept out of. Without room duties it
// grows by itself: every session seen in a protected workspace is added. With
// room duties (--os-users) it is explicit (adminlist.go): Sessions is made
// only of Pinned, the admin's own protected-workspace sessions while the
// admin's own room is "admin", and Rooms, the sessions of rooms set to admin.
type protectedList struct {
	Sessions []string `json:"sessions"`
	// Explicit marks a file kept by the explicit rules; a file without it is
	// the old self-growing list, and its ids are kept as Pinned.
	Explicit bool `json:"explicit,omitempty"`
	// Pinned are ids put on the list by hand; aiagent never removes them.
	Pinned []string `json:"pinned,omitempty"`
	// Workspace are this service's sessions seen in a protected workspace:
	// always hidden from users, admins only while the own room is admin.
	Workspace []string `json:"workspace,omitempty"`
	// Rooms holds, per room set to admin, the sessions seen in it.
	Rooms map[string][]string `json:"rooms,omitempty"`
	// Aliases maps a pinned or workspace id to the session id its background
	// job runs under now: the job keeps its short id when its conversation
	// moves on, and the guard sees the new session id.
	Aliases map[string]string `json:"aliases,omitempty"`
	// Procs maps a pinned or workspace id to the Claude Code process it was
	// last seen live in ("pid:procStart", the main service's own registry
	// only). An interactive session has no job id; after /clear the same
	// process runs under a new session id, and that id becomes its alias.
	Procs map[string]string `json:"procs,omitempty"`
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
	list, err := s.readProtected()
	if err != nil {
		return protections{all: true}
	}
	seen := s.workspaceSeen(sessions)
	changed := false
	if s.Duties != nil {
		changed = s.explicitList(&list, seen, nil, s.ownRegistry(sessions))
	} else {
		have := idSet(list.Sessions)
		for _, id := range seen {
			if !have[strings.ToLower(id)] {
				have[strings.ToLower(id)] = true
				list.Sessions = append(list.Sessions, id)
				changed = true
			}
		}
	}
	if changed {
		s.writeProtected(list)
	}
	p := protections{ids: map[string]bool{}, adminRoom: map[string]string{}}
	hidden := [][]string{list.Pinned, list.Workspace}
	if s.Duties == nil {
		hidden = append(hidden, list.Sessions)
	}
	for _, ids := range hidden {
		for _, id := range ids {
			p.ids[strings.ToLower(id)] = true
			if a := list.Aliases[id]; a != "" {
				p.ids[strings.ToLower(a)] = true
			}
		}
	}
	for room, ids := range list.Rooms {
		for _, id := range ids {
			p.adminRoom[strings.ToLower(id)] = room
		}
	}
	return p
}

func (s *Service) protectedPath() string { return filepath.Join(s.Store.dir, protectedFile) }

// readProtected reads protected.json; a missing file is an empty list.
func (s *Service) readProtected() (protectedList, error) {
	var list protectedList
	b, err := os.ReadFile(s.protectedPath())
	if errors.Is(err, os.ErrNotExist) {
		return list, nil
	}
	if err == nil {
		err = json.Unmarshal(b, &list)
	}
	return list, err
}

func (s *Service) writeProtected(list protectedList) {
	b, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return
	}
	path := s.protectedPath()
	tmp := path + ".tmp"
	if os.WriteFile(tmp, append(b, '\n'), 0o600) == nil {
		_ = os.Rename(tmp, path)
	}
}

func idSet(ids []string) map[string]bool {
	m := make(map[string]bool, len(ids))
	for _, id := range ids {
		m[strings.ToLower(id)] = true
	}
	return m
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

func (s *Service) handleMe(c fiber.Ctx) error {
	return c.JSON(okEnv(struct {
		Identity
		Preview bool `json:"preview,omitempty"`
	}{identity(c), s.OSUsers != nil && s.Preview.on()}))
}

func (s *Service) handleTokens(c fiber.Ctx) error {
	st, err := s.Store.Load()
	if err != nil {
		return s.fail(c, err)
	}
	out := []fiber.Map{}
	duties := s.Duties.Load()
	for _, u := range st.Users {
		out = append(out, fiber.Map{"id": u.ID, "name": u.Name, "createdAt": u.CreatedAt, "osUser": u.OSUser, "removing": u.Hash == "", "duty": duties[u.OSUser]})
	}
	res := fiber.Map{"tokens": out, "osUsers": s.OSUsers != nil, "duties": s.Duties != nil}
	if s.Duties != nil {
		// the admin's own token: its room is the service's own account
		res["self"] = fiber.Map{"room": s.ownRoom(), "duty": s.ownDuty(duties)}
	}
	return c.JSON(okEnv(res))
}

func (s *Service) handleTokenCreate(c fiber.Ctx) error {
	var body struct {
		Name string `json:"name"`
		Duty string `json:"duty"`
	}
	if err := c.Bind().Body(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(failEnv("AIAGENT_BAD_REQUEST", "ข้อมูลที่ส่งมาไม่ใช่ JSON ที่ถูกต้อง"))
	}
	c.Set("Cache-Control", "no-store")
	return s.mutate(c, "aiagent.tokenCreate", []string{body.Name, body.Duty}, func() (any, error) {
		if !ValidDuty(body.Duty) {
			return nil, ErrBadRequest(dutyErr)
		}
		osUser := ""
		if s.OSUsers != nil {
			osUser = strings.TrimSpace(body.Name)
			if !osUserRE.MatchString(osUser) {
				return nil, ErrBadRequest("ชื่อผู้ใช้: a-z 0-9 และขีดกลาง ขึ้นต้นด้วยตัวอักษร")
			}
			// the name becomes a path segment beside other applications
			if s.roomReserved(osUser) {
				return nil, ErrBadRequest("ชื่อนี้สงวนไว้ให้ระบบ ใช้ชื่ออื่น")
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
			if body.Duty != "" {
				if err := s.Duties.Set(osUser, body.Duty); err != nil {
					return nil, err
				}
			}
		}
		return fiber.Map{"id": u.ID, "name": u.Name, "token": token, "duty": body.Duty}, nil
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
				_ = s.Duties.Set(u.OSUser, "")
			}
		}
		if err := s.Store.RevokeUser(id); err != nil {
			return fiber.Map{"id": id}, err
		}
		// their photo faces go with them
		s.removeFaces(id)
		return fiber.Map{"id": id}, nil
	})
}

// handleTokenDuty sets what a room is for; "" clears it.
func (s *Service) handleTokenDuty(c fiber.Ctx) error {
	id := strings.Clone(c.Params("id"))
	var body struct {
		Duty string `json:"duty"`
	}
	if err := c.Bind().Body(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(failEnv("AIAGENT_BAD_REQUEST", "ข้อมูลที่ส่งมาไม่ใช่ JSON ที่ถูกต้อง"))
	}
	return s.mutate(c, "aiagent.tokenDuty", []string{id, body.Duty}, func() (any, error) {
		if s.Duties == nil {
			return nil, ErrConflict("บทบาทของห้องใช้ได้เมื่อแต่ละ token มีห้องของตัวเอง (--os-users)")
		}
		if id == "self" {
			// the admin's own room always has a duty; unset reads as admin
			if body.Duty == "" {
				return nil, ErrBadRequest(dutyErr)
			}
			if err := s.Duties.Set(s.ownRoom(), body.Duty); err != nil {
				return nil, err
			}
			s.syncAdminList(context.Background())
			return fiber.Map{"id": id, "duty": body.Duty}, nil
		}
		st, err := s.Store.Load()
		if err != nil {
			return nil, err
		}
		for _, u := range st.Users {
			if u.ID == id && u.OSUser != "" {
				if err := s.Duties.Set(u.OSUser, body.Duty); err != nil {
					return nil, err
				}
				s.syncAdminList(context.Background())
				s.kickRoomEnv() // a new duty may take environments away (environments.go)
				return fiber.Map{"id": id, "duty": body.Duty}, nil
			}
		}
		return nil, ErrNotFound("ไม่พบ token นี้")
	})
}
