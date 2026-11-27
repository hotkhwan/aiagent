package scan

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"
)

type gvcMessage struct {
	Config  *gvcConfig  `json:"config"`
	OSV     *gvcOSV     `json:"osv"`
	Finding *gvcFinding `json:"finding"`
}

type gvcConfig struct {
	DBLastModified string `json:"db_last_modified"`
}

type gvcOSV struct {
	ID      string `json:"id"`
	Summary string `json:"summary"`
}

type gvcFinding struct {
	OSV          string     `json:"osv"`
	FixedVersion string     `json:"fixed_version"`
	Trace        []gvcFrame `json:"trace"`
}

// The trace runs from the vulnerable symbol (first frame) out to the user's own
// call site (last frame).
type gvcFrame struct {
	Module   string `json:"module"`
	Version  string `json:"version"`
	Package  string `json:"package"`
	Receiver string `json:"receiver"`
	Function string `json:"function"`
	Position *struct {
		Filename string `json:"filename"`
		Line     int    `json:"line"`
	} `json:"position"`
}

// normalizeGovulncheck reads a govulncheck -json stream and returns its findings
// and the database timestamp from its config message.
func normalizeGovulncheck(stdout []byte) ([]Finding, time.Time, error) {
	dec := json.NewDecoder(bytes.NewReader(stdout))
	var (
		sawConfig bool
		dbUpdated time.Time
		titles    = map[string]string{}
		raw       []gvcFinding
	)
	for {
		var msg gvcMessage
		err := dec.Decode(&msg)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, time.Time{}, fmt.Errorf("govulncheck output: %w", err)
		}
		switch {
		case msg.Config != nil:
			sawConfig = true
			if msg.Config.DBLastModified != "" {
				t, err := time.Parse(time.RFC3339, msg.Config.DBLastModified)
				if err != nil {
					return nil, time.Time{}, fmt.Errorf("govulncheck db_last_modified: %w", err)
				}
				dbUpdated = t
			}
		case msg.OSV != nil:
			titles[msg.OSV.ID] = msg.OSV.Summary
		case msg.Finding != nil:
			raw = append(raw, *msg.Finding)
		}
	}
	if !sawConfig {
		return nil, time.Time{}, errors.New("govulncheck output has no config message")
	}

	var findings []Finding
	for _, g := range raw {
		if len(g.Trace) == 0 {
			return nil, time.Time{}, fmt.Errorf("govulncheck finding %s has no trace", g.OSV)
		}
		vuln := g.Trace[0]
		f := Finding{
			Scanner:      "govulncheck",
			VulnID:       g.OSV,
			Kind:         KindDependency,
			Module:       vuln.Module,
			Version:      vuln.Version,
			Package:      vuln.Package,
			FixedVersion: g.FixedVersion,
			Title:        titles[g.OSV],
		}
		if vuln.Function != "" {
			f.Kind = KindCode
			f.Symbol = symbolOf(vuln)
		}
		if caller := g.Trace[len(g.Trace)-1]; caller.Position != nil {
			f.File = caller.Position.Filename
			f.Line = caller.Position.Line
		}
		findings = append(findings, f)
	}
	return dedupe(findings), dbUpdated, nil
}

func symbolOf(fr gvcFrame) string {
	if fr.Receiver != "" {
		return fr.Package + "." + fr.Receiver + "." + fr.Function
	}
	return fr.Package + "." + fr.Function
}

type trivyReport struct {
	SchemaVersion int `json:"SchemaVersion"`
	Results       []struct {
		Target          string      `json:"Target"`
		Vulnerabilities []trivyVuln `json:"Vulnerabilities"`
	} `json:"Results"`
}

type trivyVuln struct {
	VulnerabilityID  string `json:"VulnerabilityID"`
	PkgName          string `json:"PkgName"`
	InstalledVersion string `json:"InstalledVersion"`
	FixedVersion     string `json:"FixedVersion"`
	Severity         string `json:"Severity"`
	Title            string `json:"Title"`
}

// normalizeTrivy reads a trivy fs --format json report.
func normalizeTrivy(stdout []byte) ([]Finding, error) {
	var rep trivyReport
	if err := json.Unmarshal(stdout, &rep); err != nil {
		return nil, fmt.Errorf("trivy output: %w", err)
	}
	if rep.SchemaVersion == 0 {
		return nil, errors.New("trivy output has no SchemaVersion; not a trivy report")
	}
	var findings []Finding
	for _, res := range rep.Results {
		for _, v := range res.Vulnerabilities {
			findings = append(findings, Finding{
				Scanner:      "trivy",
				VulnID:       v.VulnerabilityID,
				Kind:         KindDependency,
				Module:       v.PkgName,
				Version:      v.InstalledVersion,
				File:         res.Target,
				FixedVersion: v.FixedVersion,
				Severity:     v.Severity,
				Title:        v.Title,
			})
		}
	}
	return dedupe(findings), nil
}

type trivyVersion struct {
	VulnerabilityDB *struct {
		UpdatedAt string `json:"UpdatedAt"`
	} `json:"VulnerabilityDB"`
}

// parseTrivyDB reads the vulnerability database timestamp from trivy version
// --format json. A missing timestamp comes back as the zero time, which the
// caller treats as stale.
func parseTrivyDB(stdout []byte) (time.Time, error) {
	var v trivyVersion
	if err := json.Unmarshal(stdout, &v); err != nil {
		return time.Time{}, fmt.Errorf("trivy version output: %w", err)
	}
	if v.VulnerabilityDB == nil || v.VulnerabilityDB.UpdatedAt == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(time.RFC3339Nano, v.VulnerabilityDB.UpdatedAt)
	if err != nil {
		return time.Time{}, fmt.Errorf("trivy vulnerability database UpdatedAt: %w", err)
	}
	return t, nil
}

func dedupe(in []Finding) []Finding {
	var out []Finding
	seen := map[string]bool{}
	for _, f := range in {
		f.Fingerprint = Fingerprint(f)
		if seen[f.Fingerprint] {
			continue
		}
		seen[f.Fingerprint] = true
		out = append(out, f)
	}
	return out
}
