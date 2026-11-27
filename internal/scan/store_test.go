package scan

import (
	"testing"
	"time"
)

func TestStoreKeepsScansAndFindingsAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	st, err := OpenStore(dir)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	now := time.Date(2026, 11, 19, 3, 0, 0, 0, time.UTC)
	first := Scan{ScanID: "scan-1", Scanner: "trivy", Target: "/repo", Status: StatusFindings, FindingCount: 1, StartedAt: now, FinishedAt: now}
	firstFindings := []Finding{{ScanID: "scan-1", Scanner: "trivy", VulnID: "CVE-2026-2222", Kind: KindDependency, Module: "github.com/example/legacy", Version: "2.0.0", FixedVersion: "", Fingerprint: "CVE-2026-2222|dependency|github.com/example/legacy@2.0.0"}}
	if err := st.Save(first, firstFindings); err != nil {
		t.Fatalf("Save: %v", err)
	}
	second := Scan{ScanID: "scan-2", Scanner: "govulncheck", Target: "/repo", Status: StatusClean, StartedAt: now, FinishedAt: now}
	if err := st.Save(second, nil); err != nil {
		t.Fatalf("Save second: %v", err)
	}

	reopened, err := OpenStore(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	scans, err := reopened.Scans()
	if err != nil {
		t.Fatalf("Scans: %v", err)
	}
	if len(scans) != 2 {
		t.Fatalf("got %d scans; want 2", len(scans))
	}
	checkString(t, "scans[0].Status", string(scans[0].Status), string(StatusFindings))
	checkString(t, "scans[1].Status", string(scans[1].Status), string(StatusClean))

	got, err := reopened.Findings("scan-1")
	if err != nil {
		t.Fatalf("Findings: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d findings for scan-1; want 1", len(got))
	}
	checkString(t, "VulnID", got[0].VulnID, "CVE-2026-2222")
	checkString(t, "FixedVersion", got[0].FixedVersion, "")
	checkString(t, "Fingerprint", got[0].Fingerprint, firstFindings[0].Fingerprint)

	none, err := reopened.Findings("scan-2")
	if err != nil {
		t.Fatalf("Findings scan-2: %v", err)
	}
	if len(none) != 0 {
		t.Errorf("got %d findings for scan-2; want 0", len(none))
	}
}
