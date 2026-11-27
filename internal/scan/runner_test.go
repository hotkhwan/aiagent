package scan

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var testNow = time.Date(2026, 11, 19, 3, 0, 0, 0, time.UTC)

// fakeExec answers each command by its binary name and first argument:
// "govulncheck -json", "trivy fs" and "trivy version". It records every call
// as a single string so a test can check the exact argv.
type fakeExec struct {
	outs  map[string]Output
	errs  map[string]error
	calls []string
}

func (f *fakeExec) exec(ctx context.Context, dir, name string, args ...string) (Output, error) {
	f.calls = append(f.calls, strings.Join(append([]string{name}, args...), " "))
	key := filepath.Base(name) + " " + args[0]
	if err := f.errs[key]; err != nil {
		return Output{}, err
	}
	if out, ok := f.outs[key]; ok {
		return out, nil
	}
	return Output{}, fmt.Errorf("unexpected command %q", key)
}

func newFakeRunner(f *fakeExec) Runner {
	return Runner{
		GovulncheckBin: "/Users/test/go/bin/govulncheck",
		TrivyBin:       "trivy",
		MaxDBAge:       DefaultMaxDBAge,
		Exec:           f.exec,
		Now:            func() time.Time { return testNow },
	}
}

func TestGovulncheckStaleDBIsStaleNotClean(t *testing.T) {
	f := &fakeExec{outs: map[string]Output{"govulncheck -json": {Stdout: readFixture(t, "govulncheck_stale.json")}}}
	sc, _ := newFakeRunner(f).Govulncheck(context.Background(), ".")
	checkString(t, "Status", string(sc.Status), string(StatusStaleDB))
	if want := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC); !sc.DBUpdatedAt.Equal(want) {
		t.Errorf("DBUpdatedAt = %v; want %v", sc.DBUpdatedAt, want)
	}
}

func TestGovulncheckWithoutFindingsIsClean(t *testing.T) {
	f := &fakeExec{outs: map[string]Output{"govulncheck -json": {Stdout: readFixture(t, "govulncheck_clean.json")}}}
	sc, findings := newFakeRunner(f).Govulncheck(context.Background(), ".")
	checkString(t, "Status", string(sc.Status), string(StatusClean))
	if len(findings) != 0 {
		t.Errorf("got %d findings; want 0", len(findings))
	}
}

func TestGovulncheckMissingBinaryIsFailed(t *testing.T) {
	f := &fakeExec{errs: map[string]error{"govulncheck -json": exec.ErrNotFound}}
	sc, findings := newFakeRunner(f).Govulncheck(context.Background(), ".")
	checkString(t, "Status", string(sc.Status), string(StatusFailed))
	if sc.Reason == "" {
		t.Error("a failed scan must say why")
	}
	if len(findings) != 0 {
		t.Errorf("a failed scan returned %d findings; want none", len(findings))
	}
}

func TestNonZeroExitIsFailedEvenWithParseableOutput(t *testing.T) {
	f := &fakeExec{outs: map[string]Output{"govulncheck -json": {
		Stdout:   readFixture(t, "govulncheck_findings.json"),
		Stderr:   []byte("database unreachable"),
		ExitCode: 2,
	}}}
	sc, _ := newFakeRunner(f).Govulncheck(context.Background(), ".")
	checkString(t, "Status", string(sc.Status), string(StatusFailed))
}

func TestNoFixFindingIsKept(t *testing.T) {
	f := &fakeExec{outs: map[string]Output{"govulncheck -json": {Stdout: readFixture(t, "govulncheck_findings.json")}}}
	sc, findings := newFakeRunner(f).Govulncheck(context.Background(), ".")
	checkString(t, "Status", string(sc.Status), string(StatusFindings))
	if sc.FindingCount != 2 || len(findings) != 2 {
		t.Fatalf("FindingCount = %d, len = %d; want 2 and 2", sc.FindingCount, len(findings))
	}
	noFix := findings[1]
	checkString(t, "VulnID", noFix.VulnID, "GO-2026-0002")
	checkString(t, "FixedVersion", noFix.FixedVersion, "")
}

func TestTrivyMissingBinaryIsFailed(t *testing.T) {
	f := &fakeExec{errs: map[string]error{"trivy fs": exec.ErrNotFound}}
	sc, _ := newFakeRunner(f).Trivy(context.Background(), ".")
	checkString(t, "Status", string(sc.Status), string(StatusFailed))
}

func TestTrivyStaleDBIsStaleNotClean(t *testing.T) {
	f := &fakeExec{outs: map[string]Output{
		"trivy fs":      {Stdout: readFixture(t, "trivy_clean.json")},
		"trivy version": {Stdout: readFixture(t, "trivy_version_stale.json")},
	}}
	sc, _ := newFakeRunner(f).Trivy(context.Background(), ".")
	checkString(t, "Status", string(sc.Status), string(StatusStaleDB))
}

func TestTrivyUnknownDBAgeIsStale(t *testing.T) {
	f := &fakeExec{outs: map[string]Output{
		"trivy fs":      {Stdout: readFixture(t, "trivy_clean.json")},
		"trivy version": {Stdout: []byte(`{"Version": "0.76.2"}`)},
	}}
	sc, _ := newFakeRunner(f).Trivy(context.Background(), ".")
	checkString(t, "Status", string(sc.Status), string(StatusStaleDB))
}

func TestTrivyNoFixFindingIsKept(t *testing.T) {
	f := &fakeExec{outs: map[string]Output{
		"trivy fs":      {Stdout: readFixture(t, "trivy_findings.json")},
		"trivy version": {Stdout: readFixture(t, "trivy_version_fresh.json")},
	}}
	sc, findings := newFakeRunner(f).Trivy(context.Background(), ".")
	checkString(t, "Status", string(sc.Status), string(StatusFindings))
	if len(findings) != 2 {
		t.Fatalf("got %d findings; want 2", len(findings))
	}
	checkString(t, "no-fix VulnID", findings[1].VulnID, "CVE-2026-2222")
	checkString(t, "no-fix FixedVersion", findings[1].FixedVersion, "")
}

func TestRunnersUseFixedArgvWithoutShell(t *testing.T) {
	f := &fakeExec{outs: map[string]Output{
		"govulncheck -json": {Stdout: readFixture(t, "govulncheck_clean.json")},
		"trivy fs":          {Stdout: readFixture(t, "trivy_clean.json")},
		"trivy version":     {Stdout: readFixture(t, "trivy_version_fresh.json")},
	}}
	r := newFakeRunner(f)
	r.Govulncheck(context.Background(), ".")
	r.Trivy(context.Background(), ".")

	want := []string{
		"/Users/test/go/bin/govulncheck -json ./...",
		"trivy fs --quiet --format json --scanners vuln .",
		"trivy version --format json",
	}
	if len(f.calls) != len(want) {
		t.Fatalf("calls = %q; want %q", f.calls, want)
	}
	for i := range want {
		checkString(t, fmt.Sprintf("call %d", i), f.calls[i], want[i])
	}
}
