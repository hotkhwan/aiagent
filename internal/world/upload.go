// internal/aiagent/upload.go
//
// Attachments for messages. The browser uploads the raw file; this service
// stores it under the feature's state dir with a random name and returns the
// local path, which the message then references — exactly what dragging a
// file into a Claude Code / Codex terminal does (both CLIs read from a local
// path).
//
// Two kinds are taken: a picture, and a zip archive. The archive is checked to
// be one and then stored byte for byte. This service never unpacks it, never
// reads a name out of it into a path, and never serves it back to a browser;
// what happens to its contents is the agent's decision, in the agent's own
// sandbox.
package world

import (
	"archive/zip"
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	maxUploadBytes = 3_500_000
	maxZipBytes    = 50 << 20
	// MaxUploadBody is the request body the HTTP server must allow for an
	// upload to reach SaveUpload, which applies the limit of each kind.
	MaxUploadBody = maxZipBytes + 1<<20
	uploadTTL     = 7 * 24 * time.Hour
	uploadsPerMin = 30
)

var imageMagic = []struct {
	ext   string
	magic []byte
	at    int
}{
	{".png", []byte("\x89PNG\r\n\x1a\n"), 0},
	{".jpg", []byte("\xff\xd8\xff"), 0},
	{".gif", []byte("GIF8"), 0},
	{".webp", []byte("WEBP"), 8},
}

func sniffImage(b []byte) string {
	for _, m := range imageMagic {
		if len(b) >= m.at+len(m.magic) && bytes.Equal(b[m.at:m.at+len(m.magic)], m.magic) {
			if m.ext == ".webp" && !bytes.Equal(b[:4], []byte("RIFF")) {
				continue
			}
			return m.ext
		}
	}
	return ""
}

type uploadLimiter struct {
	mu    sync.Mutex
	times []time.Time
}

func (l *uploadLimiter) allow(now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	kept := l.times[:0]
	for _, t := range l.times {
		if now.Sub(t) < time.Minute {
			kept = append(kept, t)
		}
	}
	l.times = kept
	if len(kept) >= uploadsPerMin {
		return false
	}
	l.times = append(l.times, now)
	return true
}

// isZip reports whether b is a readable archive with at least one entry. The
// magic bytes alone are four characters anyone can type.
func isZip(b []byte) bool {
	if len(b) < 4 || !bytes.Equal(b[:4], []byte("PK\x03\x04")) {
		return false
	}
	r, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	return err == nil && len(r.File) > 0
}

// uploadKind names the stored extension of an accepted upload, or says why
// the bytes are refused.
func uploadKind(b []byte) (string, error) {
	if len(b) == 0 {
		return "", ErrBadRequest("ไฟล์ที่ส่งมาว่างเปล่า")
	}
	if ext := sniffImage(b); ext != "" {
		if len(b) > maxUploadBytes {
			return "", ErrBadRequest("รูปใหญ่เกิน 3.5 MB")
		}
		return ext, nil
	}
	if len(b) > maxZipBytes {
		return "", ErrBadRequest("ไฟล์ใหญ่เกิน 50 MB")
	}
	if isZip(b) {
		return ".zip", nil
	}
	return "", ErrBadRequest("รับเฉพาะรูป PNG, JPEG, GIF, WebP และไฟล์ zip")
}

// SaveUpload validates and stores one image or zip archive; it returns the
// absolute path.
func SaveUpload(dir string, b []byte, now time.Time) (string, error) {
	ext, err := uploadKind(b)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	// prune old uploads
	if ents, err := os.ReadDir(dir); err == nil {
		for _, e := range ents {
			if fi, err := e.Info(); err == nil && now.Sub(fi.ModTime()) > uploadTTL {
				_ = os.Remove(filepath.Join(dir, e.Name()))
			}
		}
	}
	rnd := make([]byte, 8)
	if _, err := rand.Read(rnd); err != nil {
		return "", err
	}
	p := filepath.Join(dir, now.UTC().Format("20060102-150405")+"-"+hex.EncodeToString(rnd)+ext)
	if err := os.WriteFile(p, b, 0o600); err != nil {
		return "", err
	}
	return p, nil
}
