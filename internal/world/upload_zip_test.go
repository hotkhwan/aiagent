package world

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// zipOf builds a real archive holding one stored (uncompressed) file.
func zipOf(t *testing.T, name string, content []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
	if err != nil {
		t.Fatalf("zip header: %v", err)
	}
	if _, err := w.Write(content); err != nil {
		t.Fatalf("zip write: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}

// A zip is attached like a picture: stored as it came, under a random name,
// readable by the owner only. The service never opens it for the agent and
// never serves it back to a browser.
func TestUploadAcceptsZip(t *testing.T) {
	e := newEnv(t)
	tok, _ := e.store.Enable("test")
	archive := zipOf(t, "notes/readme.txt", []byte("hello"))

	req := httptest.NewRequest("POST", "/aiagent/api/uploads", bytes.NewReader(archive))
	req.Header.Set(TokenHeader, tok)
	req.Header.Set("Content-Type", "application/zip")
	resp, err := e.app.Test(req)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("zip upload: %v %v", resp.StatusCode, err)
	}
	var m map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		t.Fatalf("decode: %v", err)
	}
	p := m["details"].(map[string]any)["path"].(string)
	fi, err := os.Stat(p)
	if err != nil || fi.Mode().Perm() != 0o600 || !strings.HasSuffix(p, ".zip") || !strings.HasPrefix(p, filepath.Join(e.store.dir, "uploads")) {
		t.Fatalf("stored zip %q %v %v", p, fi, err)
	}
	stored, err := os.ReadFile(p)
	if err != nil || !bytes.Equal(stored, archive) {
		t.Fatalf("the archive must be stored byte for byte: %v", err)
	}

	get := httptest.NewRequest("GET", "/aiagent/api/uploads/"+filepath.Base(p), nil)
	get.Header.Set(TokenHeader, tok)
	if resp, _ := e.app.Test(get); resp.StatusCode != 404 {
		t.Fatalf("a stored zip must not be served back: %d", resp.StatusCode)
	}
}

func TestSaveUploadLimits(t *testing.T) {
	now := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)
	png := []byte("\x89PNG\r\n\x1a\n")
	tests := []struct {
		name    string
		body    []byte
		wantExt string
		wantErr bool
	}{
		{name: "a zip larger than a picture may be", body: zipOf(t, "big.bin", make([]byte, maxUploadBytes+1)), wantExt: ".zip"},
		{name: "a zip over the zip limit", body: zipOf(t, "huge.bin", make([]byte, maxZipBytes)), wantErr: true},
		{name: "a picture over the picture limit, as before", body: append(png, make([]byte, maxUploadBytes)...), wantErr: true},
		{name: "zip magic over something that is not an archive", body: append([]byte("PK\x03\x04"), bytes.Repeat([]byte("x"), 200)...), wantErr: true},
		{name: "an empty archive", body: zipEmpty(t), wantErr: true},
		{name: "a shell script", body: []byte("#!/bin/sh\nrm -rf /"), wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := SaveUpload(t.TempDir(), tt.body, now)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("accepted as %q", p)
				}
				return
			}
			if err != nil {
				t.Fatalf("refused: %v", err)
			}
			if filepath.Ext(p) != tt.wantExt {
				t.Fatalf("stored as %q, want %s", p, tt.wantExt)
			}
		})
	}
}

func zipEmpty(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := zip.NewWriter(&buf).Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}

// The HTTP server must let a body as large as the largest upload through.
func TestUploadBodyLimitCoversTheLargestUpload(t *testing.T) {
	if MaxUploadBody < maxZipBytes || MaxUploadBody < maxUploadBytes {
		t.Fatalf("MaxUploadBody %d is below an accepted upload size", MaxUploadBody)
	}
}
