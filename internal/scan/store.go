package scan

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

const (
	scansFile    = "scans.jsonl"
	findingsFile = "findings.jsonl"
	lockFile     = "scan.lock"
)

// Store keeps scans and their findings as two append-only JSONL files, one
// record per line, guarded by an exclusive lock like taskstore's event log.
type Store struct {
	dir string
}

// OpenStore opens or creates the store in dir.
func OpenStore(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return &Store{dir: dir}, nil
}

// Save appends the findings and then the scan record. The scan line is written
// last, so a scan is only visible once all of its findings are on disk.
func (s *Store) Save(sc Scan, findings []Finding) error {
	unlock, err := s.lock()
	if err != nil {
		return err
	}
	defer unlock()
	if err := appendJSONL(filepath.Join(s.dir, findingsFile), findings); err != nil {
		return err
	}
	return appendJSONL(filepath.Join(s.dir, scansFile), []Scan{sc})
}

// Scans returns every saved scan, oldest first.
func (s *Store) Scans() ([]Scan, error) {
	unlock, err := s.lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	return readJSONL[Scan](filepath.Join(s.dir, scansFile))
}

// Findings returns the findings saved under scanID.
func (s *Store) Findings(scanID string) ([]Finding, error) {
	unlock, err := s.lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	all, err := readJSONL[Finding](filepath.Join(s.dir, findingsFile))
	if err != nil {
		return nil, err
	}
	var out []Finding
	for _, f := range all {
		if f.ScanID == scanID {
			out = append(out, f)
		}
	}
	return out, nil
}

func (s *Store) lock() (func(), error) {
	f, err := os.OpenFile(filepath.Join(s.dir, lockFile), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	return func() {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, nil
}

func appendJSONL[T any](path string, recs []T) error {
	if len(recs) == 0 {
		return nil
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	for _, r := range recs {
		if err := enc.Encode(r); err != nil {
			return err
		}
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(buf.Bytes()); err != nil {
		return err
	}
	return f.Sync()
}

func readJSONL[T any](path string) ([]T, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []T
	dec := json.NewDecoder(f)
	for {
		var r T
		err := dec.Decode(&r)
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		out = append(out, r)
	}
}
