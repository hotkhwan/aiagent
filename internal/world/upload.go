// internal/aiagent/upload.go
//
// Image attachments for messages. The browser uploads the raw image; this service
// stores it under the feature's state dir with a random name and returns the
// local path, which the message then references — exactly what dragging an
// image into a Claude Code / Codex terminal does (both CLIs read images from
// a local path).
package world

import (
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
	uploadTTL      = 7 * 24 * time.Hour
	uploadsPerMin  = 30
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

// SaveUpload validates and stores one image; it returns the absolute path.
func SaveUpload(dir string, b []byte, now time.Time) (string, error) {
	if len(b) == 0 {
		return "", ErrBadRequest("empty upload")
	}
	if len(b) > maxUploadBytes {
		return "", ErrBadRequest("image is larger than 3.5 MB")
	}
	ext := sniffImage(b)
	if ext == "" {
		return "", ErrBadRequest("only PNG, JPEG, GIF or WebP images are accepted")
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
