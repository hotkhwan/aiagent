// Package scan runs govulncheck and trivy over a directory and reduces their
// output to one shape: a Scan record with a status, and the Findings it found.
//
// A scan that cannot be trusted is never reported clean. A missing scanner, a
// non-zero exit, unreadable output or a vulnerability database older than
// MaxDBAge each end in a status other than clean.
//
// Scanners start with a fixed argv through an injectable Exec, never through a
// shell string. Records are stored as one JSON object per line, like taskstore;
// JSON keys are camelCase.
package scan

import "time"

// Status is the verdict of one scan.
type Status string

const (
	StatusClean    Status = "clean"
	StatusFindings Status = "findings"
	StatusFailed   Status = "failed"
	StatusStaleDB  Status = "staleDb"
)

// Kind says what a finding is about: code the scanner traced to a vulnerable
// symbol, or a dependency module at a version.
type Kind string

const (
	KindCode       Kind = "code"
	KindDependency Kind = "dependency"
)

// Scan is one run of one scanner over one directory.
type Scan struct {
	ScanID       string    `json:"scanId"`
	Scanner      string    `json:"scanner"`
	Target       string    `json:"target"`
	Status       Status    `json:"status"`
	Reason       string    `json:"reason,omitempty"`
	DBUpdatedAt  time.Time `json:"dbUpdatedAt"`
	StartedAt    time.Time `json:"startedAt"`
	FinishedAt   time.Time `json:"finishedAt"`
	FindingCount int       `json:"findingCount"`
}

// Finding is one vulnerability a scan reported. A finding with no fixed version
// is still a finding: FixedVersion is empty and nothing drops it.
type Finding struct {
	ScanID       string `json:"scanId"`
	Scanner      string `json:"scanner"`
	VulnID       string `json:"vulnId"`
	Kind         Kind   `json:"kind"`
	Module       string `json:"module,omitempty"`
	Version      string `json:"version,omitempty"`
	Package      string `json:"package,omitempty"`
	Symbol       string `json:"symbol,omitempty"`
	File         string `json:"file,omitempty"`
	Line         int    `json:"line,omitempty"`
	FixedVersion string `json:"fixedVersion,omitempty"`
	Severity     string `json:"severity,omitempty"`
	Title        string `json:"title,omitempty"`
	Fingerprint  string `json:"fingerprint"`
}

// Fingerprint identifies a finding across scans. A code finding is keyed by the
// vulnerable symbol and a dependency finding by module@version, so moving the
// call site or the line number does not make it look new.
func Fingerprint(f Finding) string {
	key := f.Module + "@" + f.Version
	if f.Kind == KindCode {
		key = f.Symbol
	}
	return f.VulnID + "|" + string(f.Kind) + "|" + key
}
