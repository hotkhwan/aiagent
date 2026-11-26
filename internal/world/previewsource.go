// internal/world/previewsource.go
//
// The room's half of the preview, in the room's own instance and so as the
// room's user: what to show, the files of a static folder, and filing a
// capture as a review page. Root never opens a file of a room.
//
// The source is <work>/preview.json, either
//
//	{"folder": "my-app/dist"}    a folder inside the work folder
//	{"port": 5173}               a dev server on this machine, 3000-5999
//
// and without it the newest work/*/{site,dist,public} that has an index.html.
package world

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"image/jpeg"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/gofiber/fiber/v3"
)

const (
	previewFile     = "preview.json"
	previewMaxFile  = 25 << 20
	previewMaxWalk  = 5000
	previewMaxShots = 2
)

// PreviewState is what a room shows now. Version changes when a file of a
// static folder does.
type PreviewState struct {
	Kind    string `json:"kind"` // static | port | none
	Folder  string `json:"folder,omitempty"`
	Port    int    `json:"port,omitempty"`
	Version string `json:"version,omitempty"`
	Error   string `json:"error,omitempty"`
}

func (s *Service) previewWork() string { return filepath.Join(s.Collector.serviceHome(), "work") }

// dotted reports a path with a segment that starts with a dot (which covers
// ".." and "."), or that is not a plain relative path.
func dotted(p string) bool {
	if strings.ContainsAny(p, "\\\x00") {
		return true
	}
	for _, seg := range strings.Split(p, "/") {
		if strings.HasPrefix(seg, ".") {
			return true
		}
	}
	return false
}

// openWork opens a file or folder below the work folder. os.Root keeps every
// symlink inside it, and the opened file's real path is checked again, so a
// link cannot lead to a dotfile either. Never a fifo or device.
func openWork(work, rel string) (*os.File, os.FileInfo, error) {
	rel = strings.Trim(rel, "/")
	if rel == "" || dotted(rel) || !filepath.IsLocal(rel) {
		return nil, nil, fs.ErrNotExist
	}
	real, err := filepath.EvalSymlinks(work)
	if err != nil {
		return nil, nil, err
	}
	root, err := os.OpenRoot(real)
	if err != nil {
		return nil, nil, err
	}
	defer root.Close()
	f, err := root.OpenFile(rel, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, nil, err
	}
	fi, err := f.Stat()
	if err == nil && !fi.Mode().IsRegular() && !fi.IsDir() {
		err = fs.ErrNotExist
	}
	if err == nil {
		err = checkOpened(f, real)
	}
	if err != nil {
		f.Close()
		return nil, nil, err
	}
	return f, fi, nil
}

// checkOpened asks the kernel where the open file really is; without /proc
// (macOS) the path is resolved again.
func checkOpened(f *os.File, work string) error {
	at, err := os.Readlink("/proc/self/fd/" + strconv.Itoa(int(f.Fd())))
	if err != nil {
		at, err = filepath.EvalSymlinks(f.Name())
		if err != nil {
			return err
		}
	}
	rel, err := filepath.Rel(work, at)
	if err != nil || rel == "." || !filepath.IsLocal(rel) || dotted(filepath.ToSlash(rel)) {
		return fs.ErrNotExist
	}
	return nil
}

// previewSource decides what the room shows.
func (s *Service) previewSource() PreviewState {
	work := s.previewWork()
	b, err := readWork(work, previewFile, 4<<10)
	if err == nil {
		var c struct {
			Folder string `json:"folder"`
			Port   int    `json:"port"`
		}
		d := json.NewDecoder(bytes.NewReader(b))
		d.DisallowUnknownFields()
		bad := func(why string) PreviewState { return PreviewState{Kind: "none", Error: previewFile + ": " + why} }
		switch err := d.Decode(&c); {
		case err != nil:
			return bad("not valid JSON with folder or port")
		case c.Folder != "" && c.Port != 0:
			return bad("set folder or port, not both")
		case c.Port != 0:
			if !previewPortOK(c.Port) {
				return bad(fmt.Sprintf("port must be %d-%d", previewPortMin, previewPortMax))
			}
			return PreviewState{Kind: "port", Port: c.Port}
		case c.Folder == "":
			return bad("set folder or port")
		}
		folder := strings.Trim(path.Clean(c.Folder), "/")
		if strings.HasPrefix(c.Folder, "/") || dotted(folder) {
			return bad("folder must be a plain path inside the work folder")
		}
		f, fi, err := openWork(work, folder)
		if err != nil || !fi.IsDir() {
			if err == nil {
				f.Close()
			}
			return bad("no such folder")
		}
		f.Close()
		return PreviewState{Kind: "static", Folder: folder}
	}
	if !os.IsNotExist(err) {
		return PreviewState{Kind: "none", Error: previewFile + " cannot be read"}
	}
	// the newest page a project has built
	var best PreviewState
	var at time.Time
	ents, _ := os.ReadDir(work)
	for i, e := range ents {
		if i >= 500 || dotted(e.Name()) {
			continue
		}
		for _, d := range []string{"site", "dist", "public"} {
			f, fi, err := openWork(work, e.Name()+"/"+d+"/index.html")
			if err != nil {
				continue
			}
			f.Close()
			if fi.Mode().IsRegular() && fi.ModTime().After(at) {
				at, best = fi.ModTime(), PreviewState{Kind: "static", Folder: e.Name() + "/" + d}
			}
		}
	}
	if best.Kind == "" {
		return PreviewState{Kind: "none"}
	}
	return best
}

func readWork(work, rel string, limit int64) ([]byte, error) {
	f, fi, err := openWork(work, rel)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if !fi.Mode().IsRegular() {
		return nil, fs.ErrNotExist
	}
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err == nil && int64(len(b)) > limit {
		err = &APIError{413, "AIAGENT_PREVIEW_TOO_LARGE", "file too large"}
	}
	return b, err
}

// previewVersion sums up the names, sizes and times of a folder's files. It
// does not follow symlinks; a link's target is seen when it changes itself.
func previewVersion(work, folder string) string {
	sum := sha256.New()
	fmt.Fprintln(sum, folder)
	n := 0
	root, err := filepath.EvalSymlinks(work)
	if err != nil {
		return ""
	}
	_ = filepath.WalkDir(filepath.Join(root, filepath.FromSlash(folder)), func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if n++; n > previewMaxWalk {
			return filepath.SkipAll
		}
		if strings.HasPrefix(d.Name(), ".") || d.Name() == "node_modules" {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if fi, err := d.Info(); err == nil {
			fmt.Fprintln(sum, p, fi.Size(), fi.ModTime().UnixNano())
		}
		return nil
	})
	return hex.EncodeToString(sum.Sum(nil))[:24]
}

func (s *Service) handlePreviewState(c fiber.Ctx) error {
	if s.OSUsers != nil {
		return hidden(c)
	}
	st := s.previewSource()
	if st.Kind == "static" {
		st.Version = previewVersion(s.previewWork(), st.Folder)
	}
	return c.JSON(okEnv(st))
}

// handlePreviewFile serves one file of the static folder to the admin
// service. The bytes only: their type is decided there.
func (s *Service) handlePreviewFile(c fiber.Ctx) error {
	if s.OSUsers != nil {
		return hidden(c)
	}
	st := s.previewSource()
	name := strings.Clone(c.Query("path"))
	if st.Kind != "static" || strings.HasPrefix(name, "/") || dotted(name) && name != "" {
		return hidden(c)
	}
	rel := strings.Trim(st.Folder+"/"+name, "/")
	f, fi, err := openWork(s.previewWork(), rel)
	if err != nil {
		return hidden(c)
	}
	f.Close()
	index := fi.IsDir()
	if index {
		rel += "/index.html"
	} else if strings.HasSuffix(name, "/") {
		return hidden(c)
	}
	b, err := readWork(s.previewWork(), rel, previewMaxFile)
	if err != nil {
		if ae, ok := err.(*APIError); ok {
			return s.fail(c, ae)
		}
		return hidden(c)
	}
	if index {
		c.Set("X-Preview-Index", "1")
	}
	c.Set("Cache-Control", "no-store")
	c.Set("Content-Type", "application/octet-stream")
	return c.Send(b)
}

// captureImage is one screenshot, a JPEG.
type captureImage struct {
	Width int    `json:"width"`
	Data  string `json:"data"` // base64
}

type previewReviewBody struct {
	Note   string         `json:"note"`
	Images []captureImage `json:"images"`
}

// checkCapture reports whether b is a JPEG of that width and a sane height.
func checkCapture(b []byte, width int) error {
	if len(b) == 0 || len(b) > captureMaxBytes {
		return fmt.Errorf("empty or too large")
	}
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(b))
	if err != nil || cfg.Width != width || cfg.Height < 1 || cfg.Height > captureMaxHeight {
		return fmt.Errorf("not a screenshot %d px wide", width)
	}
	return nil
}

// handlePreviewReview files a capture as this room's own review page. The
// answer goes to the room's newest live session, as with any review.
func (s *Service) handlePreviewReview(c fiber.Ctx) error {
	if s.OSUsers != nil {
		return hidden(c)
	}
	var body previewReviewBody
	if err := c.Bind().Body(&body); err != nil || len(body.Images) == 0 || len(body.Images) > previewMaxShots || len(body.Note) > 4000 {
		return c.Status(fiber.StatusBadRequest).JSON(failEnv("AIAGENT_BAD_REQUEST", "invalid JSON body"))
	}
	for _, im := range body.Images {
		b, err := base64.StdEncoding.DecodeString(im.Data)
		if err != nil || checkCapture(b, im.Width) != nil {
			return s.fail(c, ErrBadRequest("not a screenshot"))
		}
	}
	return s.mutate(c, "aiagent.previewReview", []string{strconv.Itoa(len(body.Images))}, func() (any, error) {
		var sid, runtime, cwd string
		var at time.Time
		for _, ss := range s.Collector.Snapshot(c.Context()).Sessions {
			if ss.Live && (sid == "" || ss.LastEventAt != nil && ss.LastEventAt.After(at)) {
				sid, runtime, cwd = ss.SessionID, ss.RuntimeType, ss.Cwd
				if ss.LastEventAt != nil {
					at = *ss.LastEventAt
				}
			}
		}
		src := s.previewSource()
		where := src.Folder
		if src.Kind == "port" {
			where = "port " + strconv.Itoa(src.Port)
		}
		now := s.Collector.now()
		r, err := FileReview(s.Collector.serviceHome(), "ภาพหน้าพรีวิว "+now.Local().Format("15:04"), truncateRunes(where+" "+strings.TrimSpace(body.Note), 300), sid, runtime, cwd, nil, previewReviewPage(where, body, now), now)
		if err != nil {
			return nil, err
		}
		return fiber.Map{"reviewId": r.ID}, nil
	})
}

// previewReviewPage is the page itself: one section per item to judge.
func previewReviewPage(where string, body previewReviewBody, now time.Time) []byte {
	var b strings.Builder
	b.WriteString(`<!doctype html><html lang="th"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>ภาพหน้าพรีวิว</title><style>body{margin:0 auto;max-width:1480px;padding:20px 16px;font:16px/1.6 system-ui,sans-serif;background:#f6f7fb;color:#14182b}img{display:block;max-width:100%;height:auto;margin:12px auto;border:1px solid #c9cede;border-radius:8px}section{margin:24px 0;padding:16px;background:#fff;border-radius:12px}</style></head><body>`)
	fmt.Fprintf(&b, `<h1>ภาพหน้าพรีวิว</h1><p>%s · %s</p>`, html.EscapeString(where), html.EscapeString(now.Local().Format("2006-01-02 15:04")))
	if n := strings.TrimSpace(body.Note); n != "" {
		fmt.Fprintf(&b, `<p>%s</p>`, strings.ReplaceAll(html.EscapeString(n), "\n", "<br>"))
	}
	for _, im := range body.Images {
		name := "จอเดสก์ท็อป"
		if im.Width < 600 {
			name = "จอมือถือ"
		}
		fmt.Fprintf(&b, `<section data-item="%s %d px"><h2>%s %d px</h2><img alt="%s %d px" src="data:image/jpeg;base64,%s"></section>`, name, im.Width, name, im.Width, name, im.Width, im.Data)
	}
	b.WriteString(`<section data-item="เนื้อหาและการใช้งาน"><h2>เนื้อหาและการใช้งาน</h2><p>ข้อความถูก ปุ่มและลิงก์ทำงานตามที่สั่ง</p></section></body></html>`)
	return []byte(b.String())
}
