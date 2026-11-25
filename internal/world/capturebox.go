// internal/world/capturebox.go
//
// What runs inside the capture box, as its unprivileged id: a bridge from one
// loopback port of the box's own, otherwise empty, network to the job's
// socket, and the browser driven over a pipe. Nothing here is trusted by the
// service: it checks the two files that come out.
package world

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"time"
)

const captureAddr = "127.0.0.1:9000"

var previewCapturePathRE = regexp.MustCompile(`^/dev/[a-z][a-z0-9-]{1,30}/~[0-9a-f]{48}/$`)

// chromiumArgs is the browser's whole command line.
func chromiumArgs(profile string, ownSandbox bool) []string {
	a := []string{
		"--headless", "--remote-debugging-pipe", "--user-data-dir=" + profile, "--disable-gpu", "--no-first-run", "--no-default-browser-check",
		"--disable-background-networking", "--disable-component-update", "--disable-default-apps", "--disable-extensions", "--disable-sync",
		"--disable-dev-shm-usage", "--disable-breakpad", "--hide-scrollbars", "--mute-audio", "--no-proxy-server",
	}
	if !ownSandbox {
		// the box is then the only wall: no privilege, no home, no network
		a = append(a, "--no-sandbox")
	}
	return append(a, "about:blank")
}

// RunCaptureBox takes the pictures of the page at path and writes them to out.
func RunCaptureBox(ctx context.Context, chromium, socket, path, out string) error {
	if !previewCapturePathRE.MatchString(path) {
		return fmt.Errorf("bad path")
	}
	if os.Geteuid() == 0 {
		return fmt.Errorf("refusing to run as root")
	}
	ln, err := net.Listen("tcp", captureAddr)
	if err != nil {
		return err
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				u, err := net.Dial("unix", socket)
				if err != nil {
					return
				}
				defer u.Close()
				done := make(chan struct{}, 2)
				go func() { _, _ = io.Copy(u, c); done <- struct{}{} }()
				go func() { _, _ = io.Copy(c, u); done <- struct{}{} }()
				<-done
			}()
		}
	}()
	profile, err := os.MkdirTemp("", "p")
	if err != nil {
		return err
	}
	// can a browser's own sandbox be set up here?
	own := exec.CommandContext(ctx, "/usr/bin/unshare", "--user", "--map-root-user", "--pid", "--net", "--fork", "/usr/bin/true").Run() == nil
	cmd := exec.CommandContext(ctx, chromium, chromiumArgs(profile, own)...)
	inR, inW, err := os.Pipe()
	if err != nil {
		return err
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		return err
	}
	cmd.ExtraFiles = []*os.File{inR, outW}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	inR.Close()
	outW.Close()
	b := newCDP(ctx, inW, outR)
	for _, w := range captureWidths {
		pic, err := b.shot("http://"+captureAddr+path, w)
		if err != nil {
			return fmt.Errorf("%d px: %w", w, err)
		}
		if err := os.WriteFile(filepath.Join(out, fmt.Sprintf("%d.jpg", w)), pic, 0o600); err != nil {
			return err
		}
	}
	return nil
}

// cdp speaks the browser's debugging protocol over its pipe: JSON, NUL-ended.
type cdp struct {
	ctx  context.Context
	w    io.Writer
	msgs chan cdpMsg
	next int
}

type cdpMsg struct {
	ID        int             `json:"id"`
	Method    string          `json:"method"`
	SessionID string          `json:"sessionId"`
	Result    json.RawMessage `json:"result"`
	Error     *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func newCDP(ctx context.Context, w io.Writer, r io.Reader) *cdp {
	c := &cdp{ctx: ctx, w: w, msgs: make(chan cdpMsg, 256)}
	go func() {
		defer close(c.msgs)
		br := bufio.NewReaderSize(r, 1<<20)
		for {
			line, err := br.ReadBytes(0)
			if err != nil {
				return
			}
			var m cdpMsg
			if json.Unmarshal(line[:len(line)-1], &m) == nil {
				select {
				case c.msgs <- m:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return c
}

func (c *cdp) send(session, method string, params any) (int, error) {
	c.next++
	b, err := json.Marshal(map[string]any{"id": c.next, "method": method, "params": params, "sessionId": session})
	if session == "" {
		b, err = json.Marshal(map[string]any{"id": c.next, "method": method, "params": params})
	}
	if err != nil {
		return 0, err
	}
	_, err = c.w.Write(append(b, 0))
	return c.next, err
}

// wait reads until the answer to id, or the named event, or the time is up.
func (c *cdp) wait(session string, id int, event string, limit time.Duration) (json.RawMessage, error) {
	t := time.NewTimer(limit)
	defer t.Stop()
	for {
		select {
		case <-c.ctx.Done():
			return nil, c.ctx.Err()
		case <-t.C:
			return nil, fmt.Errorf("the browser did not answer in time")
		case m, ok := <-c.msgs:
			switch {
			case !ok:
				return nil, fmt.Errorf("the browser ended")
			case m.Method == "Page.javascriptDialogOpening":
				// an alert would hold the page for ever
				_, _ = c.send(m.SessionID, "Page.handleJavaScriptDialog", map[string]any{"accept": false})
			case id != 0 && m.ID == id:
				if m.Error != nil {
					return nil, fmt.Errorf("%s", m.Error.Message)
				}
				return m.Result, nil
			case event != "" && m.Method == event && m.SessionID == session:
				return nil, nil
			}
		}
	}
}

func (c *cdp) call(session, method string, params, out any) error {
	id, err := c.send(session, method, params)
	if err != nil {
		return err
	}
	r, err := c.wait(session, id, "", 15*time.Second)
	if err != nil || out == nil {
		return err
	}
	return json.Unmarshal(r, out)
}

func (c *cdp) shot(url string, width int) ([]byte, error) {
	type m = map[string]any
	var target struct {
		TargetID string `json:"targetId"`
	}
	if err := c.call("", "Target.createTarget", m{"url": "about:blank"}, &target); err != nil {
		return nil, err
	}
	var at struct {
		SessionID string `json:"sessionId"`
	}
	if err := c.call("", "Target.attachToTarget", m{"targetId": target.TargetID, "flatten": true}, &at); err != nil {
		return nil, err
	}
	s := at.SessionID
	_ = c.call("", "Browser.setDownloadBehavior", m{"behavior": "deny"}, nil)
	height := 900
	if width < 600 {
		height = 844
	}
	if err := c.call(s, "Page.enable", m{}, nil); err != nil {
		return nil, err
	}
	if err := c.call(s, "Emulation.setDeviceMetricsOverride", m{"width": width, "height": height, "deviceScaleFactor": 1, "mobile": width < 600}, nil); err != nil {
		return nil, err
	}
	if _, err := c.send(s, "Page.navigate", m{"url": url}); err != nil {
		return nil, err
	}
	// a page that never finishes loading is shown as far as it got
	_, _ = c.wait(s, 0, "Page.loadEventFired", 10*time.Second)
	_, _ = c.wait(s, 0, "-", 1500*time.Millisecond)
	if c.ctx.Err() != nil {
		return nil, c.ctx.Err()
	}
	var size struct {
		Content struct {
			Height float64 `json:"height"`
		} `json:"cssContentSize"`
	}
	if err := c.call(s, "Page.getLayoutMetrics", m{}, &size); err != nil {
		return nil, err
	}
	h := int(size.Content.Height)
	if h < height {
		h = height
	}
	if h > captureMaxHeight {
		h = captureMaxHeight
	}
	for _, q := range []int{80, 50, 25} {
		var pic struct {
			Data string `json:"data"`
		}
		if err := c.call(s, "Page.captureScreenshot", m{"format": "jpeg", "quality": q, "captureBeyondViewport": true,
			"clip": m{"x": 0, "y": 0, "width": width, "height": h, "scale": 1}}, &pic); err != nil {
			return nil, err
		}
		b, err := base64.StdEncoding.DecodeString(pic.Data)
		if err != nil {
			return nil, err
		}
		if len(b) <= captureMaxBytes {
			_ = c.call("", "Target.closeTarget", m{"targetId": target.TargetID}, nil)
			return b, nil
		}
	}
	return nil, fmt.Errorf("the picture is too large")
}
