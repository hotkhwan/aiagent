// internal/world/devices.go
//
// Phone tokens. Pairing a phone (pair.go) mints a token for that phone alone
// instead of handing over the page's own, so a lost phone costs one revoke,
// not a rotation. A device token carries exactly the scope of the token that
// paired it: the admin's, or one room's. Only its SHA-256 is kept.
//
//	GET  /aiagent/api/connect/devices             token — the phones in the caller's scope
//	POST /aiagent/api/connect/devices/:id/revoke  token — end one of them
package world

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
)

const maxDevices = 100

var deviceIDRE = regexp.MustCompile(`^[0-9a-f]{12}$`)

// DeviceToken is a paired phone. UserID names the room whose scope it has;
// empty is the admin's.
type DeviceToken struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Hash      string    `json:"hash"`
	UserID    string    `json:"userId,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
}

// identifyDevice names the holder of a device token. A device of a room that
// is gone, or whose token is being removed, matches nothing.
func identifyDevice(st State, sum [32]byte) (Identity, bool) {
	for _, d := range st.Devices {
		if !hashMatches(d.Hash, sum) {
			continue
		}
		if d.UserID == "" {
			return Identity{Role: RoleAdmin, DeviceID: d.ID}, true
		}
		for _, u := range st.Users {
			if u.ID == d.UserID && u.Hash != "" {
				return Identity{Role: RoleUser, Name: u.Name, OSUser: u.OSUser, UserID: u.ID, DeviceID: d.ID}, true
			}
		}
		return Identity{}, false
	}
	return Identity{}, false
}

// AddDevice mints a token for a phone with the scope of id and returns it once.
func (s *Store) AddDevice(id Identity, name string) (DeviceToken, string, error) {
	name = strings.TrimSpace(name)
	if !userNameRE.MatchString(name) {
		name = "Phone"
	}
	tokensMu.Lock()
	defer tokensMu.Unlock()
	st, err := s.Load()
	if err != nil || !st.Enabled {
		return DeviceToken{}, "", ErrConflict("บริการยังไม่เปิดใช้งาน")
	}
	if len(st.Devices) >= maxDevices {
		return DeviceToken{}, "", ErrConflict("อุปกรณ์ที่จับคู่เต็มแล้ว เพิกถอนบางเครื่องก่อน")
	}
	token, err := newToken()
	if err != nil {
		return DeviceToken{}, "", err
	}
	var raw [6]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return DeviceToken{}, "", err
	}
	sum := sha256.Sum256([]byte(token))
	d := DeviceToken{ID: hex.EncodeToString(raw[:]), Name: name, Hash: hex.EncodeToString(sum[:]), CreatedAt: time.Now().UTC()}
	if id.Role == RoleUser {
		d.UserID = id.UserID
	}
	// a fresh slice: Load's result shares its array with the cache
	st.Devices = append(append([]DeviceToken{}, st.Devices...), d)
	return d, token, s.write(st)
}

// RevokeDevice ends one phone's token, if it is in the scope of by.
func (s *Store) RevokeDevice(by Identity, deviceID string) error {
	tokensMu.Lock()
	defer tokensMu.Unlock()
	st, err := s.Load()
	if err != nil {
		return err
	}
	kept := []DeviceToken{}
	found := false
	for _, d := range st.Devices {
		if d.ID == deviceID && inDeviceScope(by, d) {
			found = true
			continue
		}
		kept = append(kept, d)
	}
	if !found {
		return ErrNotFound("ไม่พบอุปกรณ์นี้")
	}
	st.Devices = kept
	return s.write(st)
}

func inDeviceScope(by Identity, d DeviceToken) bool {
	return by.Role == RoleAdmin || (by.Role == RoleUser && by.UserID != "" && d.UserID == by.UserID)
}

// devicesWithout drops the phones paired under userID ("" is the admin's).
func devicesWithout(ds []DeviceToken, userID string) []DeviceToken {
	kept := []DeviceToken{}
	for _, d := range ds {
		if d.UserID != userID {
			kept = append(kept, d)
		}
	}
	return kept
}

func (s *Service) handleDeviceList(c fiber.Ctx) error {
	c.Set("Cache-Control", "no-store")
	me := identity(c)
	st, err := s.Store.Load()
	if err != nil {
		return s.fail(c, err)
	}
	names := map[string]string{}
	for _, u := range st.Users {
		names[u.ID] = u.Name
	}
	rows := []fiber.Map{}
	for _, d := range st.Devices {
		if !inDeviceScope(me, d) {
			continue
		}
		owner := "admin"
		if d.UserID != "" {
			owner = names[d.UserID]
		}
		rows = append(rows, fiber.Map{"id": d.ID, "name": d.Name, "owner": owner, "createdAt": d.CreatedAt.UTC().Format(time.RFC3339), "current": d.ID == me.DeviceID})
	}
	return c.JSON(okEnv(fiber.Map{"devices": rows}))
}

func (s *Service) handleDeviceRevoke(c fiber.Ctx) error {
	id := strings.Clone(c.Params("id"))
	if !deviceIDRE.MatchString(id) {
		return s.fail(c, ErrNotFound("ไม่พบอุปกรณ์นี้"))
	}
	me := identity(c)
	return s.mutate(c, "aiagent.deviceRevoke", []string{id}, func() (any, error) {
		return fiber.Map{"id": id}, s.Store.RevokeDevice(me, id)
	})
}
