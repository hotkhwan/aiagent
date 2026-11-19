package scan

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return b
}

func TestNormalizeGovulncheckKeepsCodeAndDependencyFindings(t *testing.T) {
	findings, dbUpdated, err := normalizeGovulncheck(readFixture(t, "govulncheck_findings.json"))
	if err != nil {
		t.Fatalf("normalizeGovulncheck: %v", err)
	}
	if want := time.Date(2026, 11, 19, 1, 0, 0, 0, time.UTC); !dbUpdated.Equal(want) {
		t.Errorf("dbUpdated = %v; want %v", dbUpdated, want)
	}
	if len(findings) != 2 {
		t.Fatalf("got %d findings; want 2 (two call sites of one symbol collapse to one)", len(findings))
	}

	code := findings[0]
	checkString(t, "code.Scanner", code.Scanner, "govulncheck")
	checkString(t, "code.VulnID", code.VulnID, "GO-2026-0001")
	checkString(t, "code.Kind", string(code.Kind), string(KindCode))
	checkString(t, "code.Symbol", code.Symbol, "golang.org/x/net/html.Parse")
	checkString(t, "code.Module", code.Module, "golang.org/x/net")
	checkString(t, "code.Version", code.Version, "v0.1.0")
	checkString(t, "code.File", code.File, "main.go")
	if code.Line != 10 {
		t.Errorf("code.Line = %d; want 10 (the user's call site)", code.Line)
	}
	checkString(t, "code.FixedVersion", code.FixedVersion, "v1.2.3")
	checkString(t, "code.Title", code.Title, "Parse panics on crafted input")

	dep := findings[1]
	checkString(t, "dep.VulnID", dep.VulnID, "GO-2026-0002")
	checkString(t, "dep.Kind", string(dep.Kind), string(KindDependency))
	checkString(t, "dep.Module", dep.Module, "example.com/old")
	checkString(t, "dep.Version", dep.Version, "v0.0.9")
	checkString(t, "dep.Symbol", dep.Symbol, "")
	checkString(t, "dep.FixedVersion", dep.FixedVersion, "")
}

func TestNormalizeTrivyReadsVulnerabilitiesAndNullResults(t *testing.T) {
	findings, err := normalizeTrivy(readFixture(t, "trivy_findings.json"))
	if err != nil {
		t.Fatalf("normalizeTrivy: %v", err)
	}
	if len(findings) != 2 {
		t.Fatalf("got %d findings; want 2", len(findings))
	}
	first := findings[0]
	checkString(t, "Scanner", first.Scanner, "trivy")
	checkString(t, "VulnID", first.VulnID, "CVE-2026-1111")
	checkString(t, "Kind", string(first.Kind), string(KindDependency))
	checkString(t, "Module", first.Module, "github.com/example/parser")
	checkString(t, "Version", first.Version, "1.0.0")
	checkString(t, "File", first.File, "go.mod")
	checkString(t, "Severity", first.Severity, "HIGH")
	checkString(t, "FixedVersion", first.FixedVersion, "1.0.1")
}

func TestNormalizeRejectsGovulncheckWithoutConfig(t *testing.T) {
	_, _, err := normalizeGovulncheck([]byte(`{"progress": {"message": "hi"}}`))
	if err == nil {
		t.Fatal("expected an error: a stream with no config message cannot prove the database age")
	}
}

func TestFingerprintIgnoresLocationForCodeFindings(t *testing.T) {
	base := Finding{VulnID: "GO-2026-0001", Kind: KindCode, Symbol: "golang.org/x/net/html.Parse", Module: "golang.org/x/net", Version: "v0.1.0", File: "main.go", Line: 10, FixedVersion: "v1.2.3"}
	moved := base
	moved.File, moved.Line, moved.FixedVersion = "handler.go", 99, ""
	if Fingerprint(base) != Fingerprint(moved) {
		t.Errorf("fingerprint changed when only location and fix changed: %q vs %q", Fingerprint(base), Fingerprint(moved))
	}
}

func TestFingerprintKeysDependencyByModuleAndVersion(t *testing.T) {
	old := Finding{VulnID: "CVE-2026-1111", Kind: KindDependency, Module: "github.com/example/parser", Version: "1.0.0"}
	upgraded := old
	upgraded.Version = "1.0.1"
	if Fingerprint(old) == Fingerprint(upgraded) {
		t.Errorf("a dependency at a new version must not share the old fingerprint")
	}
	sameVersion := old
	sameVersion.Line = 42
	if Fingerprint(old) != Fingerprint(sameVersion) {
		t.Errorf("fingerprint changed for the same module@version")
	}
}

func TestFingerprintSeparatesVulnerabilities(t *testing.T) {
	a := Finding{VulnID: "GO-2026-0001", Kind: KindCode, Symbol: "golang.org/x/net/html.Parse"}
	b := a
	b.VulnID = "GO-2026-0003"
	if Fingerprint(a) == Fingerprint(b) {
		t.Errorf("two vulnerabilities on the same symbol must have different fingerprints")
	}
}

func checkString(t *testing.T, name, got, want string) {
	t.Helper()
	if got != want {
		t.Errorf("%s = %q; want %q", name, got, want)
	}
}
