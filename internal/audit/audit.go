// internal/audit/audit.go
package audit

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Record is one append-only audit line (contract §Audit). Secrets
// (passphrase, bootstrap token, grant signature) are NEVER written — callers
// pass only a params digest.
type Record struct {
	TS         string `json:"ts"`
	Event      string `json:"event"` // "intent" | "result"
	RPC        string `json:"rpc"`
	AdminSub   string `json:"adminSub,omitempty"`
	GrantID    string `json:"grantTokenId,omitempty"`
	GrantNonce string `json:"grantNonce,omitempty"`
	ParamsHash string `json:"paramsHash,omitempty"`
	JobID      string `json:"jobId,omitempty"`
	Result     string `json:"result,omitempty"` // "ok" | grpc code string
	Err        string `json:"err,omitempty"`
}

// Logger appends JSONL records to a single file opened O_APPEND. Append-only
// at the file-permission/logrotate layer is an ops concern; the daemon only
// ever appends.
type Logger struct {
	mu sync.Mutex
	f  *os.File
}

// Open creates the audit file (and parent dir) if needed, 0640.
func Open(path string) (*Logger, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("audit dir: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o640)
	if err != nil {
		return nil, fmt.Errorf("audit open: %w", err)
	}
	return &Logger{f: f}, nil
}

// Write appends one record, stamping TS. An audit-write failure is returned
// to the caller: the contract maps it to INTERNAL — a host action must not
// proceed unaudited.
func (l *Logger) Write(r Record) error {
	r.TS = time.Now().UTC().Format(time.RFC3339Nano)
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, err := l.f.Write(append(b, '\n')); err != nil {
		return err
	}
	return l.f.Sync()
}

// Close flushes and closes the underlying file.
func (l *Logger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.f.Close()
}
