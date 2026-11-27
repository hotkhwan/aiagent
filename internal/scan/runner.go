package scan

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// DefaultMaxDBAge is how old a vulnerability database may be before a scan
// that used it is reported as staleDb.
const DefaultMaxDBAge = 48 * time.Hour

// Output is what a finished command wrote. ExitCode is set even when it is
// non-zero; Exec returns an error only when the command could not start.
type Output struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
}

// Exec runs name with args in dir and returns what it wrote.
type Exec func(ctx context.Context, dir, name string, args ...string) (Output, error)

// Runner runs the scanners. The zero value uses the scanners found on PATH.
type Runner struct {
	GovulncheckBin string        // path to the binary; "" means "govulncheck"
	TrivyBin       string        // path to the binary; "" means "trivy"
	MaxDBAge       time.Duration // zero means DefaultMaxDBAge
	Exec           Exec          // nil means ExecCommand
	Now            func() time.Time
}

// ExecCommand starts name with args directly, without a shell.
func ExecCommand(ctx context.Context, dir, name string, args ...string) (Output, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	out := Output{Stdout: stdout.Bytes(), Stderr: stderr.Bytes()}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		out.ExitCode = exitErr.ExitCode()
		return out, nil
	}
	return out, err
}

// Govulncheck runs govulncheck over every package under dir.
func (r Runner) Govulncheck(ctx context.Context, dir string) (Scan, []Finding) {
	sc := r.begin("govulncheck", dir)
	out, err := r.run(ctx, dir, orDefault(r.GovulncheckBin, "govulncheck"), "-json", "./...")
	if err != nil {
		return r.failed(sc, err)
	}
	findings, dbUpdated, err := normalizeGovulncheck(out.Stdout)
	if err != nil {
		return r.failed(sc, err)
	}
	sc.DBUpdatedAt = dbUpdated
	return r.finish(sc, findings, r.dbStale(dbUpdated))
}

// Trivy runs trivy's filesystem vulnerability scan over dir.
func (r Runner) Trivy(ctx context.Context, dir string) (Scan, []Finding) {
	sc := r.begin("trivy", dir)
	bin := orDefault(r.TrivyBin, "trivy")
	out, err := r.run(ctx, dir, bin, "fs", "--quiet", "--format", "json", "--scanners", "vuln", ".")
	if err != nil {
		return r.failed(sc, err)
	}
	findings, err := normalizeTrivy(out.Stdout)
	if err != nil {
		return r.failed(sc, err)
	}
	version, err := r.run(ctx, dir, bin, "version", "--format", "json")
	if err != nil {
		return r.failed(sc, err)
	}
	dbUpdated, err := parseTrivyDB(version.Stdout)
	if err != nil {
		return r.failed(sc, err)
	}
	sc.DBUpdatedAt = dbUpdated
	return r.finish(sc, findings, r.dbStale(dbUpdated))
}

func (r Runner) run(ctx context.Context, dir, name string, args ...string) (Output, error) {
	exe := r.Exec
	if exe == nil {
		exe = ExecCommand
	}
	out, err := exe(ctx, dir, name, args...)
	if err != nil {
		return out, err
	}
	if out.ExitCode != 0 {
		return out, fmt.Errorf("%s exited %d: %s", filepath.Base(name), out.ExitCode, firstChars(strings.TrimSpace(string(out.Stderr)), 200))
	}
	return out, nil
}

func (r Runner) begin(scanner, dir string) Scan {
	return Scan{ScanID: "scan-" + rand.Text(), Scanner: scanner, Target: dir, StartedAt: r.now()}
}

func (r Runner) failed(sc Scan, err error) (Scan, []Finding) {
	sc.Status = StatusFailed
	sc.Reason = err.Error()
	sc.FinishedAt = r.now()
	return sc, nil
}

func (r Runner) finish(sc Scan, findings []Finding, stale bool) (Scan, []Finding) {
	for i := range findings {
		findings[i].ScanID = sc.ScanID
	}
	sc.FindingCount = len(findings)
	sc.FinishedAt = r.now()
	switch {
	case stale:
		sc.Status = StatusStaleDB
		sc.Reason = fmt.Sprintf("vulnerability database older than %s or its age is unknown", r.maxAge())
	case len(findings) > 0:
		sc.Status = StatusFindings
	default:
		sc.Status = StatusClean
	}
	return sc, findings
}

func (r Runner) dbStale(updated time.Time) bool {
	if updated.IsZero() {
		return true
	}
	return r.now().Sub(updated) > r.maxAge()
}

func (r Runner) maxAge() time.Duration {
	if r.MaxDBAge == 0 {
		return DefaultMaxDBAge
	}
	return r.MaxDBAge
}

func (r Runner) now() time.Time {
	if r.Now == nil {
		return time.Now()
	}
	return r.Now()
}

func orDefault(bin, fallback string) string {
	if bin == "" {
		return fallback
	}
	return bin
}

func firstChars(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
