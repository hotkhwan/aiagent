// internal/world/push.go
//
// Push notifications to the phone app, through a relay.
//
// The app registers with the relay (aiagent-push) and gets a handle: a random
// value bound to that phone and this server. It gives the handle to this
// service with its own token (POST /push/devices). Every 15 seconds this
// service works out what waits for each token's holder (an agent's question,
// an agent blocked for instructions, an open review page, a finished turn), and posts each NEW
// item once to the relay, which turns it into a notification with fixed text.
//
// What leaves this machine per item: the handle, the kind, the session or page
// name and its id. Never a token, never what an agent said.
//
// A device is bound to the SHA-256 of the token that registered it. Revoking,
// reissuing or rotating that token stops its pushes at the next tick, and the
// device is forgotten.
package world

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/gofiber/fiber/v3"
)

// DefaultPushRelay is the relay the phone app registers with. A server that
// runs its own relay, or wants none, sets --push-relay.
const DefaultPushRelay = "https://aiagent-push.hotkhwan.workers.dev"

const (
	pushFile       = "push.json"
	pushTick       = 15 * time.Second
	pushMaxDevices = 200
	// pushPerToken: one token's phones; the oldest gives way to a new one, so
	// one token can never fill the slots everyone shares
	pushPerToken   = 5
	pushMaxPerTick = 5
)

var pushHandleRE = regexp.MustCompile(`^aph_[A-Za-z0-9_-]{43}$`)

// PushItem is one thing waiting for a person.
type PushItem struct {
	Kind string // answer | waiting | review
	Key  string // stable while the same thing waits
	Name string
	Ref  string
	// Look is the agent's character, shown as the notification's picture.
	Look string
}

// webChars are the characters the web gives an agent that has none chosen
// (app.js `chars`), picked by webLook from its id.
var webChars = []string{"dev", "tester", "infra", "deploy", "research", "designer"}

// webLook is app.js `chars[hash(id) % chars.length]`: 32-bit, multiply by 31.
func webLook(id string) string {
	var x uint32
	for _, c := range []byte(id) {
		x = x*31 + uint32(c)
	}
	return webChars[x%uint32(len(webChars))]
}

func lookOf(ss Session) string {
	if ss.Look != "" {
		return ss.Look
	}
	return webLook(ss.SessionID)
}

type pushDevice struct {
	Handle    string    `json:"handle"`
	TokenHash string    `json:"tokenHash"`
	AddedAt   time.Time `json:"addedAt"`
}

type pushState struct {
	mu sync.Mutex
	// seen: per handle, the item keys already pushed (or waiting when the
	// device was first seen). A missing handle has not been seen yet.
	seen map[string]map[string]bool
}

// ParsePushRelay checks a --push-relay value: an https origin, or "off"/""
// for none. Plain http is taken for loopback only (tests, a local relay).
func ParsePushRelay(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "off" {
		return "", nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.Hostname() == "" || u.User != nil {
		return "", fmt.Errorf("push relay %q is not an https address", raw)
	}
	loop := u.Hostname() == "127.0.0.1" || u.Hostname() == "localhost" || u.Hostname() == "::1"
	if u.Scheme != "https" && !(u.Scheme == "http" && loop) {
		return "", fmt.Errorf("push relay %q must use https", raw)
	}
	return u.Scheme + "://" + u.Host, nil
}

func digestHex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// pushItems lists what waits for a person in a snapshot already cut to what
// they may see, and the review pages they may see.
func pushItems(snap Snapshot, reviews []Review) []PushItem {
	var out []PushItem
	looks := map[string]string{}
	for _, ss := range snap.Sessions {
		looks[ss.SessionID] = lookOf(ss)
	}
	for _, ss := range snap.Sessions {
		if ss.Protected && ss.MessageBlocked != "" {
			continue // an admin's session shown to a user: nothing to act on
		}
		switch {
		case ss.Prompt != nil:
			var b strings.Builder
			b.WriteString(ss.Prompt.Question)
			for _, o := range ss.Prompt.Options {
				fmt.Fprintf(&b, "\x00%d %s", o.N, o.Label)
			}
			out = append(out, PushItem{Kind: "answer", Key: "a|" + ss.SessionID + "|" + digestHex(b.String())[:16], Name: ss.DisplayName, Ref: ss.SessionID, Look: lookOf(ss)})
		case ss.AgentState == "blocked":
			out = append(out, PushItem{Kind: "waiting", Key: "w|" + ss.SessionID, Name: ss.DisplayName, Ref: ss.SessionID, Look: lookOf(ss)})
		case ss.Live && (ss.Status == StatusIdle || ss.Status == StatusCompleted) && ss.LastEventAt != nil:
			// a finished turn: each one is its own item, keyed by when it ended,
			// so the next finished turn notifies again
			out = append(out, PushItem{Kind: "done", Key: "d|" + ss.SessionID + "|" + ss.LastEventAt.UTC().Format(time.RFC3339Nano), Name: ss.DisplayName, Ref: ss.SessionID, Look: lookOf(ss)})
		}
	}
	for _, r := range reviews {
		if r.Status == "open" {
			look := looks[r.SessionID]
			if look == "" && r.SessionID != "" {
				look = webLook(r.SessionID)
			}
			out = append(out, PushItem{Kind: "review", Key: "r|" + r.ID, Name: r.Title, Ref: r.ID, Look: look})
		}
	}
	return out
}

// --- devices -------------------------------------------------------------

var pushFileMu sync.Mutex

func (s *Service) pushPath() string { return filepath.Join(s.Store.dir, pushFile) }

func (s *Service) pushDevices() []pushDevice {
	pushFileMu.Lock()
	defer pushFileMu.Unlock()
	return s.readPushDevices()
}

func (s *Service) readPushDevices() []pushDevice {
	b, err := os.ReadFile(s.pushPath())
	if err != nil {
		return nil
	}
	var d []pushDevice
	if json.Unmarshal(b, &d) != nil {
		return nil
	}
	return d
}

func (s *Service) writePushDevices(d []pushDevice) error {
	if err := os.MkdirAll(s.Store.dir, 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.pushPath() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.pushPath())
}

// addPushDevice binds a handle to the token presented with it. A handle
// registered again moves to the new token.
func (s *Service) addPushDevice(handle, token string) error {
	if !pushHandleRE.MatchString(handle) {
		return ErrBadRequest("handle ไม่ถูกต้อง")
	}
	pushFileMu.Lock()
	defer pushFileMu.Unlock()
	cur := s.readPushDevices()
	out := make([]pushDevice, 0, len(cur)+1)
	for _, d := range cur {
		if d.Handle != handle {
			out = append(out, d)
		}
	}
	th := digestHex(token)
	mine := 0
	for _, d := range out {
		if d.TokenHash == th {
			mine++
		}
	}
	for mine >= pushPerToken {
		oldest := -1
		for i, d := range out {
			if d.TokenHash == th && (oldest < 0 || d.AddedAt.Before(out[oldest].AddedAt)) {
				oldest = i
			}
		}
		out = append(out[:oldest], out[oldest+1:]...)
		mine--
	}
	if len(out) >= pushMaxDevices {
		return ErrConflict("ลงทะเบียนอุปกรณ์เต็มแล้ว")
	}
	out = append(out, pushDevice{Handle: handle, TokenHash: th, AddedAt: time.Now().UTC()})
	return s.writePushDevices(out)
}

// removePushDevices drops the devices for which drop is true.
func (s *Service) removePushDevices(drop func(pushDevice) bool) error {
	pushFileMu.Lock()
	defer pushFileMu.Unlock()
	cur := s.readPushDevices()
	out := make([]pushDevice, 0, len(cur))
	for _, d := range cur {
		if !drop(d) {
			out = append(out, d)
		}
	}
	if len(out) == len(cur) {
		return nil
	}
	return s.writePushDevices(out)
}

func pushHandleOf(c fiber.Ctx) (string, error) {
	var body struct {
		Handle string `json:"handle"`
	}
	if err := json.Unmarshal(c.Body(), &body); err != nil || !pushHandleRE.MatchString(body.Handle) {
		return "", ErrBadRequest(`expected {"handle": "aph_…"}`)
	}
	return body.Handle, nil
}

func (s *Service) handlePushDeviceAdd(c fiber.Ctx) error {
	h, err := pushHandleOf(c)
	if err != nil {
		return s.fail(c, err)
	}
	token := strings.Clone(c.Get(TokenHeader))
	// the audit log gets a digest: a handle can send to a phone
	return s.mutate(c, "aiagent.pushDevice", []string{digestHex(h)}, func() (any, error) {
		return fiber.Map{"registered": true}, s.addPushDevice(h, token)
	})
}

// handlePushDeviceRemove removes a device registered with the caller's own
// token; an admin may remove any. Unknown handles are not an error.
func (s *Service) handlePushDeviceRemove(c fiber.Ctx) error {
	h, err := pushHandleOf(c)
	if err != nil {
		return s.fail(c, err)
	}
	mine := digestHex(c.Get(TokenHeader))
	admin := identity(c).Role == RoleAdmin
	return s.mutate(c, "aiagent.pushDeviceRemove", []string{digestHex(h)}, func() (any, error) {
		return fiber.Map{"removed": true}, s.removePushDevices(func(d pushDevice) bool {
			return d.Handle == h && (admin || d.TokenHash == mine)
		})
	})
}

// --- the tick ------------------------------------------------------------

// StartPush runs PushTick every 15 seconds while a relay is set.
func (s *Service) StartPush(ctx context.Context) {
	go func() {
		t := time.NewTicker(pushTick)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				s.PushTick(ctx)
			}
		}
	}()
}

// PushTick sends what is new since the last tick to every device.
func (s *Service) PushTick(ctx context.Context) {
	if s.PushRelay == "" || !s.Store.Enabled() {
		return
	}
	devs := s.pushDevices()
	if len(devs) == 0 {
		return
	}
	source := s.pushSource
	if source == nil {
		source = s.defaultPushSource
	}
	type who struct {
		id    Identity
		ok    bool
		items []PushItem
		err   error
	}
	byToken := map[string]*who{}
	gone := map[string]bool{}
	for _, d := range devs {
		w := byToken[d.TokenHash]
		if w == nil {
			w = &who{}
			if sum, err := hex.DecodeString(d.TokenHash); err == nil && len(sum) == sha256.Size {
				w.id, w.ok = s.Store.IdentifyHash([sha256.Size]byte(sum))
			}
			if w.ok {
				w.items, w.err = source(ctx, w.id)
			}
			byToken[d.TokenHash] = w
		}
		if !w.ok {
			gone[d.Handle] = true // its token was revoked, reissued or rotated
			continue
		}
		if w.err != nil {
			continue // read again next tick; nothing is marked seen
		}
		if s.pushDevice(ctx, d.Handle, w.items) {
			gone[d.Handle] = true
		}
	}
	if len(gone) > 0 {
		_ = s.removePushDevices(func(d pushDevice) bool { return gone[d.Handle] })
		s.push.mu.Lock()
		for h := range gone {
			delete(s.push.seen, h)
		}
		s.push.mu.Unlock()
	}
}

// pushDevice sends the new items to one device; true means the relay says
// the device is gone.
func (s *Service) pushDevice(ctx context.Context, handle string, items []PushItem) bool {
	s.push.mu.Lock()
	if s.push.seen == nil {
		s.push.seen = map[string]map[string]bool{}
	}
	seen, known := s.push.seen[handle]
	now := map[string]bool{}
	var fresh []PushItem
	for _, it := range items {
		now[it.Key] = true
		if known && !seen[it.Key] {
			fresh = append(fresh, it)
		}
	}
	// What waited when the device was first seen is not pushed: the app
	// shows it on opening. Afterwards only a new item is.
	keep := map[string]bool{}
	for k := range now {
		if !known || seen[k] {
			keep[k] = true
		}
	}
	s.push.seen[handle] = keep
	s.push.mu.Unlock()

	if len(fresh) > pushMaxPerTick {
		fresh = fresh[:pushMaxPerTick]
	}
	for _, it := range fresh {
		status, err := s.sendPush(ctx, handle, it)
		if status == http.StatusNotFound || status == http.StatusGone {
			return true
		}
		if err != nil || status >= 300 {
			continue // not marked seen: tried again next tick
		}
		s.push.mu.Lock()
		if m := s.push.seen[handle]; m != nil {
			m[it.Key] = true
		}
		s.push.mu.Unlock()
	}
	return false
}

var pushClient = &http.Client{
	Timeout:       10 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

func (s *Service) sendPush(ctx context.Context, handle string, it PushItem) (int, error) {
	body := map[string]string{"handle": handle, "kind": it.Kind, "name": it.Name, "ref": it.Ref}
	if it.Look != "" {
		body["look"] = it.Look
	}
	b, err := json.Marshal(body)
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", s.PushRelay+"/v1/notify", bytes.NewReader(b))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := pushClient.Do(req)
	if err != nil {
		return 0, err
	}
	resp.Body.Close()
	return resp.StatusCode, nil
}

// defaultPushSource reads what one identity may see, the way the web does.
func (s *Service) defaultPushSource(ctx context.Context, id Identity) ([]PushItem, error) {
	if s.OSUsers != nil && id.Role == RoleUser {
		return s.instancePushItems(ctx, id.OSUser)
	}
	snap := s.Collector.Snapshot(ctx)
	snap.Sessions = s.applyNames(snap.Sessions)
	snap.nonNil()
	snap = s.forRole(id, snap)
	rs := s.Collector.Reviews()
	if id.Role == RoleAdmin {
		if more, err := s.roomReviews(ctx); err == nil {
			rs = append(rs, more...)
		}
	} else {
		p := s.protections(ctx)
		kept := []Review{}
		for _, r := range rs {
			if r.SessionID != "" && !p.covers(r.SessionID) {
				kept = append(kept, r)
			}
		}
		rs = kept
	}
	return pushItems(snap, rs), nil
}

// instancePushItems asks a user's own instance, as the web does for them.
func (s *Service) instancePushItems(ctx context.Context, osUser string) ([]PushItem, error) {
	if osUser == "" {
		return nil, nil
	}
	read := func(path string, into any) error {
		status, _, b, err := s.OSUsers.do(ctx, osUser, "GET", apiPrefix+path, "", nil)
		if err != nil {
			return err
		}
		if status != http.StatusOK {
			return fmt.Errorf("instance %s: %s answered %d", osUser, path, status)
		}
		var env struct {
			Details json.RawMessage `json:"details"`
		}
		if err := json.Unmarshal(b, &env); err != nil {
			return err
		}
		return json.Unmarshal(env.Details, into)
	}
	var snap Snapshot
	if err := read("/snapshot", &snap); err != nil {
		return nil, err
	}
	var rv struct {
		Reviews []Review `json:"reviews"`
	}
	if err := read("/reviews", &rv); err != nil {
		return nil, err
	}
	return pushItems(snap, rv.Reviews), nil
}
