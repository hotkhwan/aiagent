// internal/world/roomspool.go
//
// How a room's agent reaches its own instance from inside Claude Code's
// command sandbox. With CLAUDE_CODE_SUBPROCESS_ENV_SCRUB=1 every command the
// agent runs is wrapped in bubblewrap, which binds ~/.claude, ~/.claude.json
// and the shell's start-up files read-only and blocks unix sockets. The agent
// can still write its work folder, so it leaves a request there:
//
//	~/work/.aiagent/in/<32 hex>.review    a review page to file
//	~/work/.aiagent/in/<32 hex>.capture   a screenshot of the room's page
//	~/work/.aiagent/out/<32 hex>.json     the instance's answer
//	~/work/.aiagent/captures/*.jpg        the screenshots
//
// The room's own instance, which runs as the room's user outside that
// sandbox, collects them. It is the agent's own user, so this gives the agent
// nothing it could not do as itself, except that the instance writes
// ~/.claude/aiagent-reviews: that is exactly what the sandbox forbids, so every
// step here refuses links, hard links, files of another owner and anything
// that is not plain, and the instance writes only under names it chose.
//
// A capture needs the admin service, which alone may start the box. It asks
// each room's instance, over that room's root-owned socket, whether a capture
// is wanted; the room is the one whose socket it dialled, never a name the
// room gave. Root never opens a file of a room: the images go back over the
// same socket and the instance writes them as the room's user.
package world

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/gofiber/fiber/v3"

	"github.com/hotkhwan/aiagent/internal/audit"
)

const (
	spoolDir        = ".aiagent"
	spoolMaxReview  = reviewMaxHTML/3*4 + 64<<10 // the page as base64, and the rest
	spoolMaxCapture = 1 << 10
	spoolMaxScan    = 256
	spoolReplyKeep  = time.Hour
	spoolCaptureTTL = 3 * time.Minute
	spoolCaptureMax = 40 // files kept in captures/
)

var (
	spoolIDRE      = regexp.MustCompile(`^[0-9a-f]{32}$`)
	spoolNameRE    = regexp.MustCompile(`^([0-9a-f]{32})\.(review|capture)$`)
	spoolTmpRE     = regexp.MustCompile(`^\.[0-9a-f]{32}\.[a-z]+\.[0-9a-f]{8}\.tmp$`)
	spoolSessionRE = regexp.MustCompile(`^[A-Za-z0-9-]{0,80}$`)
	reviewTagRE    = regexp.MustCompile(`<[A-Za-z!]`)
	errSpoolUnsafe = errors.New("not a plain file or folder of this user")
)

// SpoolRequest is a review page as the CLI leaves it.
type SpoolRequest struct {
	Title     string      `json:"title"`
	Summary   string      `json:"summary,omitempty"`
	SessionID string      `json:"sessionId,omitempty"`
	Runtime   string      `json:"runtime,omitempty"`
	Cwd       string      `json:"cwd,omitempty"`
	Code      *ReviewCode `json:"code,omitempty"`
	Page      []byte      `json:"page"` // base64 in the file
}

// SpoolReply is the instance's answer to one request.
type SpoolReply struct {
	ID       string   `json:"id"`
	ReviewID string   `json:"reviewId,omitempty"`
	Files    []string `json:"files,omitempty"`
	Error    string   `json:"error,omitempty"`
}

// ---------- opening without following ----------

func ownedBy(fi fs.FileInfo, uid int) bool {
	st, ok := fi.Sys().(*syscall.Stat_t)
	return ok && int(st.Uid) == uid && fi.Mode().Perm()&0o002 == 0
}

// openOwnDir opens name below parent: a real folder of this user, never a
// link, and still the same folder once opened.
func openOwnDir(parent *os.Root, name string, create bool) (*os.Root, error) {
	if create {
		if err := parent.Mkdir(name, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
			return nil, err
		}
	}
	fi, err := parent.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !fi.IsDir() || !ownedBy(fi, os.Geteuid()) {
		return nil, errSpoolUnsafe
	}
	r, err := parent.OpenRoot(name)
	if err != nil {
		return nil, err
	}
	if st, err := r.Stat("."); err != nil || !os.SameFile(fi, st) {
		r.Close()
		return nil, errSpoolUnsafe
	}
	return r, nil
}

// openSpool opens <home>/work/.aiagent, and creates it when asked.
func openSpool(home string, create bool) (*os.Root, error) {
	fi, err := os.Lstat(home)
	if err != nil {
		return nil, err
	}
	if !fi.IsDir() || !ownedBy(fi, os.Geteuid()) {
		return nil, errSpoolUnsafe
	}
	h, err := os.OpenRoot(home)
	if err != nil {
		return nil, err
	}
	defer h.Close()
	if st, err := h.Stat("."); err != nil || !os.SameFile(fi, st) {
		return nil, errSpoolUnsafe
	}
	w, err := openOwnDir(h, "work", false)
	if err != nil {
		return nil, err
	}
	defer w.Close()
	return openOwnDir(w, spoolDir, create)
}

// readOwnFile reads a plain file of this user with one name, no link to it.
func readOwnFile(r *os.Root, name string, limit int64) ([]byte, fs.FileInfo, error) {
	fi, err := r.Lstat(name)
	if err != nil {
		return nil, nil, err
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !fi.Mode().IsRegular() || !ownedBy(fi, os.Geteuid()) || !ok || st.Nlink != 1 || fi.Mode().Perm()&0o022 != 0 {
		return nil, nil, errSpoolUnsafe
	}
	if fi.Size() > limit {
		return nil, nil, fmt.Errorf("too large")
	}
	f, err := r.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	if at, err := f.Stat(); err != nil || !os.SameFile(fi, at) {
		return nil, nil, errSpoolUnsafe
	}
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err == nil && int64(len(b)) > limit {
		err = fmt.Errorf("too large")
	}
	return b, fi, err
}

// writeNewFile writes data under name: first to a fresh file no one else
// could have made, then renamed over name, which replaces a link rather than
// following it.
func writeNewFile(r *os.Root, name string, data []byte) error {
	var rb [4]byte
	_, _ = rand.Read(rb[:])
	tmp := "." + name + "." + hex.EncodeToString(rb[:]) + ".tmp"
	f, err := r.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = r.Rename(tmp, name)
	}
	if err != nil {
		_ = r.Remove(tmp)
	}
	return err
}

func newSpoolID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// checkReviewPage is what a page must be to be filed from the spool: text,
// with at least one tag.
func checkReviewPage(page []byte) error {
	if len(page) == 0 || len(page) > reviewMaxHTML {
		return fmt.Errorf("page must be 1 byte to %d MB", reviewMaxHTML>>20)
	}
	if !utf8.Valid(page) || bytes.IndexByte(page, 0) >= 0 {
		return fmt.Errorf("page must be UTF-8 text")
	}
	if !reviewTagRE.Match(page) {
		return fmt.Errorf("page has no HTML tags")
	}
	return nil
}

// ---------- the CLI's side ----------

// spoolPut leaves one request in the room's spool.
func spoolPut(home, kind string, body []byte) (string, error) {
	sp, err := openSpool(home, true)
	if err != nil {
		return "", fmt.Errorf("%s/work/%s: %w", home, spoolDir, err)
	}
	defer sp.Close()
	for _, d := range []string{"in", "out", "captures"} {
		r, err := openOwnDir(sp, d, true)
		if err != nil {
			return "", err
		}
		r.Close()
	}
	// nothing in it is for git or the preview
	_ = writeNewFile(sp, ".gitignore", []byte("*\n"))
	in, err := openOwnDir(sp, "in", false)
	if err != nil {
		return "", err
	}
	defer in.Close()
	id := newSpoolID()
	return id, writeNewFile(in, id+"."+kind, body)
}

// SpoolReview leaves a review page for the room's instance to file.
func SpoolReview(home string, req SpoolRequest) (string, error) {
	if strings.TrimSpace(req.Title) == "" {
		return "", fmt.Errorf("--title is required")
	}
	if err := checkReviewPage(req.Page); err != nil {
		return "", err
	}
	b, err := json.Marshal(req)
	if err != nil {
		return "", err
	}
	return spoolPut(home, "review", b)
}

// SpoolCapture asks for screenshots of the room's page.
func SpoolCapture(home string) (string, error) {
	return spoolPut(home, "capture", []byte("{}\n"))
}

// SpoolWait waits for the instance's answer to id and removes it.
func SpoolWait(ctx context.Context, home, id string) (*SpoolReply, error) {
	if !spoolIDRE.MatchString(id) {
		return nil, fmt.Errorf("bad id")
	}
	t := time.NewTicker(300 * time.Millisecond)
	defer t.Stop()
	for {
		if sp, err := openSpool(home, false); err == nil {
			out, err := openOwnDir(sp, "out", false)
			sp.Close()
			if err == nil {
				b, _, err := readOwnFile(out, id+".json", 64<<10)
				if err == nil {
					_ = out.Remove(id + ".json")
				}
				out.Close()
				if err == nil {
					var r SpoolReply
					if json.Unmarshal(b, &r) != nil || r.ID != id {
						return nil, fmt.Errorf("unreadable answer")
					}
					return &r, nil
				}
			}
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-t.C:
		}
	}
}

// ---------- the room's instance ----------

func (s *Service) spoolHome() string { return s.Collector.serviceHome() }

// StartSpool collects the spool of this instance's own work folder. Only a
// room's (or a single user's) instance: the admin service has rooms instead.
func (s *Service) StartSpool(ctx context.Context) {
	if s.OSUsers != nil {
		return
	}
	go func() {
		t := time.NewTicker(time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if s.Store.Enabled() {
					s.collectSpool(s.Collector.now())
				}
			}
		}
	}()
}

type spoolEntry struct {
	id, kind string
	fi       fs.FileInfo
}

// spoolScan lists what in/ holds, oldest first, removing stale temporaries.
func spoolScan(in *os.Root, now time.Time) []spoolEntry {
	f, err := in.Open(".")
	if err != nil {
		return nil
	}
	ents, _ := f.ReadDir(spoolMaxScan)
	f.Close()
	var out []spoolEntry
	for _, e := range ents {
		fi, err := in.Lstat(e.Name())
		if err != nil {
			continue
		}
		if spoolTmpRE.MatchString(e.Name()) && fi.Mode().IsRegular() && now.Sub(fi.ModTime()) > 10*time.Minute {
			_ = in.Remove(e.Name())
			continue
		}
		if m := spoolNameRE.FindStringSubmatch(e.Name()); m != nil {
			out = append(out, spoolEntry{id: m[1], kind: m[2], fi: fi})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].fi.ModTime().Before(out[j].fi.ModTime()) })
	return out
}

// spoolAnswer writes the reply to id and drops the request.
func spoolAnswer(sp, in *os.Root, name string, r SpoolReply) error {
	out, err := openOwnDir(sp, "out", true)
	if err != nil {
		return err
	}
	defer out.Close()
	b, _ := json.Marshal(r)
	if err := writeNewFile(out, r.ID+".json", b); err != nil {
		return err
	}
	return in.Remove(name)
}

func (s *Service) spoolAudit(event, rpc, id string, err error) error {
	if s.Auditor == nil {
		return nil
	}
	rec := audit.Record{Event: event, RPC: rpc, AdminSub: "room-agent", ParamsHash: paramsHash(id)}
	if event == "result" {
		rec.Result = "ok"
		if err != nil {
			rec.Result, rec.Err = "error", err.Error()
		}
	}
	return s.Auditor.Write(rec)
}

// collectSpool files the reviews waiting, expires old capture requests and
// prunes answers nobody fetched.
func (s *Service) collectSpool(now time.Time) {
	home := s.spoolHome()
	sp, err := openSpool(home, false)
	if err != nil {
		return
	}
	defer sp.Close()
	in, err := openOwnDir(sp, "in", false)
	if err != nil {
		return
	}
	defer in.Close()
	for _, e := range spoolScan(in, now) {
		name := e.id + "." + e.kind
		switch e.kind {
		case "review":
			s.spoolReview(sp, in, home, name, e.id, now)
		case "capture":
			if now.Sub(e.fi.ModTime()) > spoolCaptureTTL {
				_ = spoolAnswer(sp, in, name, SpoolReply{ID: e.id, Error: "no capture was made in time; try again"})
			}
		}
	}
	if out, err := openOwnDir(sp, "out", false); err == nil {
		pruneSpool(out, now, spoolReplyKeep, 0)
		out.Close()
	}
	if c, err := openOwnDir(sp, "captures", false); err == nil {
		pruneSpool(c, now, 24*time.Hour, spoolCaptureMax)
		c.Close()
	}
}

// pruneSpool removes plain files older than keep, and the oldest beyond max.
func pruneSpool(r *os.Root, now time.Time, keep time.Duration, max int) {
	f, err := r.Open(".")
	if err != nil {
		return
	}
	ents, _ := f.ReadDir(4 * spoolMaxScan)
	f.Close()
	type file struct {
		name string
		at   time.Time
	}
	var files []file
	for _, e := range ents {
		fi, err := r.Lstat(e.Name())
		if err != nil || !(fi.Mode().IsRegular() || fi.Mode()&fs.ModeSymlink != 0) {
			continue
		}
		if now.Sub(fi.ModTime()) > keep {
			_ = r.Remove(e.Name())
			continue
		}
		files = append(files, file{e.Name(), fi.ModTime()})
	}
	if max > 0 && len(files) > max {
		sort.Slice(files, func(i, j int) bool { return files[i].at.After(files[j].at) })
		for _, f := range files[max:] {
			_ = r.Remove(f.name)
		}
	}
}

func (s *Service) spoolReview(sp, in *os.Root, home, name, id string, now time.Time) {
	b, _, err := readOwnFile(in, name, spoolMaxReview)
	if errors.Is(err, errSpoolUnsafe) {
		// a link or a hard link: never read through it, and never remove
		// what it points at; the name alone goes
		_ = spoolAnswer(sp, in, name, SpoolReply{ID: id, Error: "the request is not a plain file of this room"})
		_ = in.Remove(name)
		return
	}
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		_ = spoolAnswer(sp, in, name, SpoolReply{ID: id, Error: "the request is unreadable: " + err.Error()})
		return
	}
	if err != nil {
		return
	}
	if aerr := s.spoolAudit("intent", "aiagent.spoolReview", id, nil); aerr != nil {
		return // left for the next pass
	}
	r, err := fileSpooledReview(home, b, now)
	_ = s.spoolAudit("result", "aiagent.spoolReview", id, err)
	reply := SpoolReply{ID: id}
	if err != nil {
		reply.Error = err.Error()
	} else {
		reply.ReviewID = r.ID
		s.Collector.invalidate()
	}
	_ = spoolAnswer(sp, in, name, reply)
}

func fileSpooledReview(home string, b []byte, now time.Time) (*Review, error) {
	var req SpoolRequest
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err := d.Decode(&req); err != nil {
		return nil, fmt.Errorf("the request is not a review")
	}
	if err := checkReviewPage(req.Page); err != nil {
		return nil, err
	}
	if !spoolSessionRE.MatchString(req.SessionID) {
		req.SessionID = ""
	}
	if req.Runtime != "claudeCode" && req.Runtime != "codexCli" {
		req.Runtime = ""
	}
	// the folder a resumed session would start in: inside this home only
	cwd := filepath.Clean(req.Cwd)
	if rel, err := filepath.Rel(home, cwd); !filepath.IsAbs(req.Cwd) || err != nil || !filepath.IsLocal(rel) && rel != "." {
		cwd = filepath.Join(home, "work")
	}
	return FileReview(home, req.Title, req.Summary, req.SessionID, req.Runtime, cwd, req.Code, req.Page, now)
}

// handleCaptureWanted tells the admin service the oldest capture this room's
// agent asked for. It names an id, nothing else.
func (s *Service) handleCaptureWanted(c fiber.Ctx) error {
	if s.OSUsers != nil {
		return hidden(c)
	}
	id := ""
	if sp, err := openSpool(s.spoolHome(), false); err == nil {
		if in, err := openOwnDir(sp, "in", false); err == nil {
			now := s.Collector.now()
			for _, e := range spoolScan(in, now) {
				if e.kind != "capture" || now.Sub(e.fi.ModTime()) > spoolCaptureTTL {
					continue
				}
				if _, _, err := readOwnFile(in, e.id+".capture", spoolMaxCapture); err == nil {
					id = e.id
					break
				}
			}
			in.Close()
		}
		sp.Close()
	}
	return c.JSON(okEnv(fiber.Map{"id": id}))
}

type captureResult struct {
	ID     string         `json:"id"`
	Images []captureImage `json:"images"`
	Error  string         `json:"error"`
}

// handleCaptureResult stores what the admin service captured for a request of
// this room, as this room's user, under names chosen here.
func (s *Service) handleCaptureResult(c fiber.Ctx) error {
	if s.OSUsers != nil {
		return hidden(c)
	}
	var body captureResult
	if err := c.Bind().Body(&body); err != nil || !spoolIDRE.MatchString(body.ID) || len(body.Error) > 500 {
		return s.fail(c, ErrBadRequest("invalid JSON body"))
	}
	var pics [][]byte
	if body.Error == "" {
		if len(body.Images) != len(captureWidths) {
			return s.fail(c, ErrBadRequest("not a capture"))
		}
		for i, im := range body.Images {
			b, err := decodeCapture(im, captureWidths[i])
			if err != nil {
				return s.fail(c, ErrBadRequest("not a capture"))
			}
			pics = append(pics, b)
		}
	}
	sp, err := openSpool(s.spoolHome(), false)
	if err != nil {
		return hidden(c)
	}
	defer sp.Close()
	in, err := openOwnDir(sp, "in", false)
	if err != nil {
		return hidden(c)
	}
	defer in.Close()
	name := body.ID + ".capture"
	if _, _, err := readOwnFile(in, name, spoolMaxCapture); err != nil {
		return hidden(c)
	}
	_ = s.spoolAudit("intent", "aiagent.spoolCapture", body.ID, nil)
	reply := SpoolReply{ID: body.ID, Error: truncateRunes(body.Error, 300)}
	if reply.Error == "" {
		reply.Files, err = s.saveCaptures(sp, body.ID, pics)
		if err != nil {
			reply.Files, reply.Error = nil, "the pictures could not be saved: "+err.Error()
		}
	}
	err = spoolAnswer(sp, in, name, reply)
	if err == nil && reply.Error != "" {
		err = errors.New(reply.Error)
	}
	_ = s.spoolAudit("result", "aiagent.spoolCapture", body.ID, err)
	return c.JSON(okEnv(nil))
}

func decodeCapture(im captureImage, width int) ([]byte, error) {
	if im.Width != width {
		return nil, fmt.Errorf("width")
	}
	b, err := base64.StdEncoding.DecodeString(im.Data)
	if err != nil {
		return nil, err
	}
	return b, checkCapture(b, width)
}

func (s *Service) saveCaptures(sp *os.Root, id string, pics [][]byte) ([]string, error) {
	dir, err := openOwnDir(sp, "captures", true)
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	stamp := s.Collector.now().Local().Format("20060102-150405") + "-" + id[:8]
	var files []string
	for i, b := range pics {
		name := fmt.Sprintf("%s-%d.jpg", stamp, captureWidths[i])
		if err := writeNewFile(dir, name, b); err != nil {
			return nil, err
		}
		files = append(files, filepath.Join(s.spoolHome(), "work", spoolDir, "captures", name))
	}
	return files, nil
}

// ---------- the admin service ----------

const (
	roomCapturePoll   = 3 * time.Second
	roomCapturePerHr  = 20
	roomCaptureBackup = 2 * time.Minute // a room that cannot answer is asked again later
)

type roomCaptureState struct {
	sync.Mutex
	busy    map[string]bool
	recent  map[string][]time.Time
	backoff map[string]time.Time
}

// init makes the maps. rc is locked.
func (rc *roomCaptureState) init() {
	if rc.busy == nil {
		rc.busy, rc.recent, rc.backoff = map[string]bool{}, map[string][]time.Time{}, map[string]time.Time{}
	}
}

// StartRoomCaptures asks every room, now and then, whether its agent wants a
// capture, and makes one at a time for each.
func (s *Service) StartRoomCaptures(ctx context.Context) {
	if s.OSUsers == nil {
		return
	}
	go func() {
		t := time.NewTicker(roomCapturePoll)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if s.Store.Enabled() {
					s.pollRoomCaptures(ctx, time.Now())
				}
			}
		}
	}()
}

func (s *Service) pollRoomCaptures(ctx context.Context, now time.Time) {
	rc := &s.roomCap
	for _, room := range s.roomNames() {
		rc.Lock()
		rc.init()
		skip := rc.busy[room] || now.Before(rc.backoff[room])
		if !skip {
			rc.busy[room] = true
		}
		rc.Unlock()
		if skip {
			continue
		}
		go func() {
			defer func() {
				rc.Lock()
				delete(rc.busy, room)
				rc.Unlock()
			}()
			s.serveRoomCapture(ctx, room, now)
		}()
	}
}

// serveRoomCapture answers one room's capture request, if it has one.
func (s *Service) serveRoomCapture(ctx context.Context, room string, now time.Time) {
	rc := &s.roomCap
	rc.Lock()
	rc.init()
	rc.Unlock()
	later := func(d time.Duration) {
		rc.Lock()
		rc.backoff[room] = now.Add(d)
		rc.Unlock()
	}
	cctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	status, _, b, err := s.OSUsers.do(cctx, room, "GET", apiPrefix+"/preview/capture/wanted", "", nil)
	cancel()
	var env struct {
		Details struct {
			ID string `json:"id"`
		} `json:"details"`
	}
	if err != nil || status != 200 || json.Unmarshal(b, &env) != nil {
		later(roomCaptureBackup)
		return
	}
	id := env.Details.ID
	if id == "" {
		return
	}
	if !spoolIDRE.MatchString(id) {
		later(roomCaptureBackup)
		return
	}
	res := captureResult{ID: id}
	p := s.Preview
	g, granted := pvGrant{}, false
	if p.on() {
		g, granted = p.roomGrant(room)
	}
	rc.Lock()
	kept := rc.recent[room][:0]
	for _, t := range rc.recent[room] {
		if now.Sub(t) < time.Hour {
			kept = append(kept, t)
		}
	}
	rc.recent[room] = kept
	full := len(kept) >= roomCapturePerHr
	rc.Unlock()
	switch {
	case !p.on() || p.capture == nil:
		res.Error = "capture is not available on this machine"
	case !granted:
		res.Error = "this room has no page"
	case full:
		res.Error = fmt.Sprintf("at most %d captures an hour; try again later", roomCapturePerHr)
	default:
		if err := p.audit("intent", "aiagent.roomCapture", g, nil); err != nil {
			return // nothing unaudited; asked again on the next tick
		}
		shots, err := p.captureRoom(ctx, room, g)
		if ae, ok := err.(*APIError); ok && ae.Status == 429 {
			_ = p.audit("result", "aiagent.roomCapture", g, err)
			return // another capture is running; this one waits its turn
		}
		_ = p.audit("result", "aiagent.roomCapture", g, err)
		rc.Lock()
		rc.recent[room] = append(rc.recent[room], now)
		rc.Unlock()
		if err != nil {
			res.Error = err.Error()
			if ae, ok := err.(*APIError); ok {
				res.Error = ae.Msg
			}
		} else {
			res.Images = shots
		}
	}
	body, _ := json.Marshal(res)
	pctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if _, _, _, err := s.OSUsers.do(pctx, room, "POST", apiPrefix+"/preview/capture/result", "application/json", body); err != nil {
		later(roomCaptureBackup)
	}
}

// roomGrant is the room's own standing to have its page captured: the same
// check a viewer's grant is held to, with no viewer.
func (p *Preview) roomGrant(room string) (pvGrant, bool) {
	if !osUserRE.MatchString(room) || p.reserved[room] {
		return pvGrant{}, false
	}
	st, err := p.svc.Store.Load()
	if err != nil {
		return pvGrant{}, false
	}
	g := pvGrant{room: room, who: "room-agent:" + room}
	for _, u := range st.Users {
		if u.OSUser == room && u.Hash != "" {
			g.roomID, g.roomHash = u.ID, u.Hash
		}
	}
	return g, p.holds(g)
}
