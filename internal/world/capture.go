// internal/world/capture.go
//
// A screenshot of a room's page, made on this machine. The browser renders
// what a room wrote, so it runs as neither root nor the room: packaging/
// capture-box.sh, started through a systemd unit with a fixed argv, boxes it
// as an unprivileged id with no home and no network. Its only way out is one
// unix socket, on which this service answers that one room's content.
package world

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const (
	captureMaxBytes  = 1 << 20 // two of them fit a review page
	captureMaxHeight = 6000
	captureTimeout   = 75 * time.Second
	// CaptureDir holds one folder per capture while it runs, root only.
	CaptureDir = "/run/aiagent-capture"
)

// captureWidths are what a page is checked at.
var captureWidths = []int{390, 1440}

// capturer runs one capture at a time.
type capturer struct {
	dir     string
	runner  Runner
	timeout time.Duration
	busy    chan struct{}
}

// EnableCapture offers captures when the configured browser exists.
func (p *Preview) EnableCapture(dir string, r Runner) bool {
	fi, err := os.Stat(p.cfg.Chromium)
	if p.cfg.Chromium == "" || err != nil || !fi.Mode().IsRegular() || fi.Mode()&0o111 == 0 || !filepath.IsAbs(p.cfg.Chromium) {
		return false
	}
	p.capture = &capturer{dir: dir, runner: r, timeout: captureTimeout, busy: make(chan struct{}, 1)}
	return true
}

// captureArgv is the whole command; only the job's own random id varies.
func captureArgv(id string) (string, []string) {
	return "systemctl", []string{"start", "aiagent-capture@" + id + ".service"}
}

func (c *capturer) run(ctx context.Context, room string, serve func(base string) http.Handler) ([]captureImage, error) {
	failed := func(why string) error {
		return &APIError{502, "AIAGENT_CAPTURE_FAILED", "the page could not be captured: " + why}
	}
	select {
	case c.busy <- struct{}{}:
		defer func() { <-c.busy }()
	default:
		return nil, &APIError{429, "AIAGENT_SLOW_DOWN", "another capture is running, try again in a moment"}
	}
	var rb [40]byte
	if _, err := rand.Read(rb[:]); err != nil {
		return nil, err
	}
	id, secret := hex.EncodeToString(rb[:16]), hex.EncodeToString(rb[16:])
	base := "/dev/" + room + "/~" + secret + "/"
	job := filepath.Join(c.dir, id)
	if err := os.Mkdir(job, 0o700); err != nil {
		return nil, failed("no working folder")
	}
	defer os.RemoveAll(job)
	if err := os.WriteFile(filepath.Join(job, "path"), []byte(base), 0o600); err != nil {
		return nil, failed("no working folder")
	}
	ln, err := net.Listen("unix", filepath.Join(job, "s"))
	if err != nil {
		return nil, failed("no socket")
	}
	srv := &http.Server{Handler: serve(base), ReadHeaderTimeout: 10 * time.Second, MaxHeaderBytes: 64 << 10}
	go func() { _ = srv.Serve(ln) }()
	defer srv.Close()

	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	name, args := captureArgv(id)
	if out, err := c.runner.Run(ctx, name, args...); err != nil {
		if ctx.Err() != nil {
			// the unit must not outlive its job
			stop, done := context.WithTimeout(context.Background(), 10*time.Second)
			defer done()
			_, _ = c.runner.Run(stop, "systemctl", "stop", "aiagent-capture@"+id+".service")
			return nil, failed("it took too long")
		}
		return nil, failed(truncateRunes(strings.TrimSpace(string(out)), 200))
	}
	var imgs []captureImage
	for _, w := range captureWidths {
		b, err := readCapture(filepath.Join(job, fmt.Sprintf("%d.jpg", w)))
		if err == nil {
			err = checkCapture(b, w)
		}
		if err != nil {
			return nil, failed("no usable picture")
		}
		imgs = append(imgs, captureImage{Width: w, Data: base64.StdEncoding.EncodeToString(b)})
	}
	return imgs, nil
}

func readCapture(path string) ([]byte, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if fi, err := f.Stat(); err != nil || !fi.Mode().IsRegular() || fi.Size() > captureMaxBytes {
		return nil, fmt.Errorf("not a picture")
	}
	return io.ReadAll(io.LimitReader(f, captureMaxBytes+1))
}
