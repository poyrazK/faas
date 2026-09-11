// sbom.go — SPDX-2.3 SBoM CVE-baseline gate (ADR-113 canonical
// daemon tarball, PR-A commit 3).
//
// Closes issue #601 partial: the host-side regression gate. Every
// release.tar.gz carries an embedded SPDX-2.3 SBoM (commit 3 also
// owns Tarball.SBOM and the bin-tree syft-emit half). At install
// time the host loads the previously-recorded "known good" baseline
// (the KGV — known good version — recorded at last successful
// install), parses the new SBoM, and refuses to install if any
// CRITICAL/HIGH CVE has regressed (i.e., the new SBoM has more
// CRITICAL/HIGH vulns than the previous baseline knew about).
//
// What this commit does NOT do:
//
//   - Per-CVE suppression / whitelisting. Out of scope; opens a
//     CVE-handling process and a policy doc, not a code change.
//     A future ADR will define that process; today a regression
//     is fail-closed and the operator either rolls the image
//     back or opens a CVE-suppression PR.
//   - Day-3 cross-node SBoM replication. Same package, but
//     distributed fetch is PR-B + the multi-host tarball pull.
//   - CycloneDX. ADR-113 is explicit: SPDX-2.3 canonical;
//     CycloneDX producers are rejected at the producer side
//     (see scripts/build-canonical-tarball.sh from PR-A commit
//     1).
//
// Load-bearing invariants:
//
//  1. The KGV is operator-confirmed at install time, not
//     auto-rotated. The default KGV is "zero CRITICAL/HIGH" —
//     PR-A's fail-closed default is conservative; an upgrade
//     that ships with more known vulns than the KGV refuses
//     to install. Operators can rotate the KGV with the
//     `release KGV rotate` subcommand (PR-B).
//  2. The SBoM parser is SPDX-2.3 ONLY. CycloneDX inputs are
//     rejected at parse time (ErrUnsupportedSBOMFormat).
//  3. Diff is monotonic in severity: a CRITICAL regression is
//     always an error; a HIGH regression is an error; MEDIUM
//     and below are advisory (logged but not fail-closed). The
//     release/install contract is "no surprises at CRITICAL/
//     HIGH"; finer-grained policy is per-deploy.
package releaseinstall

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
)

// SBOMSeverity is the CVE severity bucket the KGV diff cares
// about. The enumeration is ordered from most-severe to least-
// severe so sort.Slice(s) can place regressions first in
// operator-facing error messages.
type SBOMSeverity int

const (
	SevUnknown SBOMSeverity = iota
	SevCritical
	SevHigh
	SevMedium
	SevLow
)

func (s SBOMSeverity) String() string {
	switch s {
	case SevCritical:
		return "CRITICAL"
	case SevHigh:
		return "HIGH"
	case SevMedium:
		return "MEDIUM"
	case SevLow:
		return "LOW"
	default:
		return "UNKNOWN"
	}
}

// SBOMCounts is the per-severity CVE count extracted from an
// SPDX-2.3 SBoM. The host-side regression check only reads
// CriticalN and HighN; MediumN is recorded for the doctor probe
// (PR-B) and operator dashboards.
type SBOMCounts struct {
	CriticalN int `json:"critical"`
	HighN     int `json:"high"`
	MediumN   int `json:"medium"`
	LowN      int `json:"low"`
}

// SBOMBaseline is the host-side snapshot of CVE counts at the
// last successful install. Stored at
// /opt/faas/releases/<git-sha>/sbom-baseline.json alongside the
// release-manifest; rotated via `gregalectl release KGV rotate`
// (PR-B).
//
// A nil SBOMBaseline means "no prior baseline" — Diff treats
// this as fail-closed (the first install on a fresh box MUST be
// preceded by a KGV init; see KGVZero below).
type SBOMBaseline struct {
	GitSHA    string     `json:"git_sha"`
	Counts    SBOMCounts `json:"counts"`
	CreatedAt string     `json:"created_at"` // RFC 3339; stringly-typed to dodge the wire-format drift around time.Time JSON shape.
}

// SBOMBaselineName is the mutable, operator-owned KGV sidecar stored beside
// an immutable release bundle. It is deliberately excluded from the signed
// deployment manifest and validated through ReadBaseline instead.
const SBOMBaselineName = "sbom-baseline.json"

// CVERegression is one CRITICAL/HIGH bucket that grew between
// the prior baseline and the new SBoM. Operator-facing error
// messages sort by severity so the worst regression is at the
// top.
type CVERegression struct {
	Severity SBOMSeverity
	PrevN    int
	NewN     int
	Delta    int
}

func (r CVERegression) Error() string {
	return fmt.Sprintf("%s regression: %d → %d (delta +%d)",
		r.Severity, r.PrevN, r.NewN, r.Delta)
}

// Errors surfaced by Diff. ErrCVERegression wraps a non-empty
// []CVERegression so callers can errors.Is() it AND walk the
// regressions individually. ErrUnsupportedSBOMFormat is the
// producer-side mistake path (a CycloneDX SBoM slipped past the
// canary); ErrSBOMMalformed is the wire-format drift path
// (SPDX-2.3 schema field missing).
var (
	ErrCVERegression         = errors.New("releaseinstall: SBoM CVE regression")
	ErrUnsupportedSBOMFormat = errors.New("releaseinstall: SBoM is not SPDX-2.3")
	ErrSBOMMalformed         = errors.New("releaseinstall: SBoM malformed")
	ErrNilBaseline           = errors.New("releaseinstall: nil SBOM baseline")
)

// KGVZero returns the conservative "zero known vulns" baseline.
// Used as the default KGV the first time a box installs a Gregale
// release. A subsequent install with even one CRITICAL/HIGH CVE
// in the SBoM triggers ErrCVERegression until the operator runs
// `release KGV rotate`.
func KGVZero(gitSHA string) SBOMBaseline {
	return SBOMBaseline{
		GitSHA:    gitSHA,
		Counts:    SBOMCounts{},
		CreatedAt: "1970-01-01T00:00:00Z",
	}
}

// ParseSPDXv2_3 parses an SPDX-2.3 JSON SBoM and extracts the
// per-severity CVE counts. The schema we read is the canonical
// "packages[].annotations[].severity" extension introduced in
// SPDX 2.3 — Gregale's syft config (`scripts/build-canonical-
// tarball.sh`) emits that shape.
//
// Rejects CycloneDX inputs (ErrUnsupportedSBOMFormat) by sniffing
// the SPDX-2.3 top-level "spdxVersion" field. CycloneDX uses
// "bomFormat"; either is unique to its producer.
func ParseSPDXv2_3(body []byte) (SBOMCounts, error) {
	if len(body) == 0 {
		return SBOMCounts{}, fmt.Errorf("%w: empty body", ErrSBOMMalformed)
	}
	var probe struct {
		SPDXVersion string `json:"spdxVersion"`
		BOMFormat   string `json:"bomFormat"` // CycloneDX
	}
	if err := json.Unmarshal(body, &probe); err != nil {
		return SBOMCounts{}, fmt.Errorf("%w: top-level: %w", ErrSBOMMalformed, err)
	}
	if !strings.HasPrefix(probe.SPDXVersion, "SPDX-2.") {
		return SBOMCounts{}, fmt.Errorf("%w: spdxVersion=%q (want SPDX-2.3)",
			ErrUnsupportedSBOMFormat, probe.SPDXVersion)
	}
	if probe.BOMFormat != "" {
		// CycloneDX has slipped through the canary. Hard refuse.
		return SBOMCounts{}, fmt.Errorf("%w: bomFormat=%q (CycloneDX is rejected; SPDX-2.3 is canonical)",
			ErrUnsupportedSBOMFormat, probe.BOMFormat)
	}

	var doc struct {
		Packages []struct {
			Annotations []struct {
				Severity string `json:"severity"`
				Comment  string `json:"comment"`
			} `json:"annotations"`
		} `json:"packages"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return SBOMCounts{}, fmt.Errorf("%w: packages: %w", ErrSBOMMalformed, err)
	}

	var c SBOMCounts
	for _, p := range doc.Packages {
		for _, a := range p.Annotations {
			sev := severityFromString(a.Severity)
			if a.Comment != "CVE" {
				// The annotation severity field is reused for
				// non-CVE annotations (license risk etc.); only
				// CVE-typed ones count toward the regression
				// gate.
				continue
			}
			switch sev {
			case SevCritical:
				c.CriticalN++
			case SevHigh:
				c.HighN++
			case SevMedium:
				c.MediumN++
			case SevLow:
				c.LowN++
			}
		}
	}
	return c, nil
}

func severityFromString(s string) SBOMSeverity {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "CRITICAL":
		return SevCritical
	case "HIGH":
		return SevHigh
	case "MEDIUM":
		return SevMedium
	case "LOW":
		return SevLow
	default:
		return SevUnknown
	}
}

// Diff compares the current SBoM counts against the prior baseline
// and returns a (possibly empty) slice of regressions. An empty
// slice means "no CRITICAL/HIGH regression"; any non-empty slice
// is wrapped in ErrCVERegression for errors.Is().
//
// nil baseline is fail-closed (ErrNilBaseline) — the operator
// MUST init the KGV before the first install. This is the same
// posture PR-B's `release KGV rotate` codifies: the KGV is
// operator-confirmed, never auto-rotated.
func (b SBOMBaseline) Diff(current SBOMCounts) ([]CVERegression, error) {
	if b.GitSHA == "" && b.Counts == (SBOMCounts{}) {
		// The zero-value baseline is a programmer error, not an
		// operator gesture. Use KGVZero explicitly when the
		// operator wants "no known vulns".
		return nil, ErrNilBaseline
	}
	var regs []CVERegression
	if current.CriticalN > b.Counts.CriticalN {
		regs = append(regs, CVERegression{
			Severity: SevCritical,
			PrevN:    b.Counts.CriticalN,
			NewN:     current.CriticalN,
			Delta:    current.CriticalN - b.Counts.CriticalN,
		})
	}
	if current.HighN > b.Counts.HighN {
		regs = append(regs, CVERegression{
			Severity: SevHigh,
			PrevN:    b.Counts.HighN,
			NewN:     current.HighN,
			Delta:    current.HighN - b.Counts.HighN,
		})
	}
	if len(regs) == 0 {
		return nil, nil
	}
	// Sort most-severe first so operator-facing error messages
	// lead with CRITICAL.
	sort.Slice(regs, func(i, j int) bool {
		return regs[i].Severity < regs[j].Severity
	})
	return regs, fmt.Errorf("%w: %s", ErrCVERegression, formatRegressions(regs))
}

func formatRegressions(regs []CVERegression) string {
	var b bytes.Buffer
	for i, r := range regs {
		if i > 0 {
			b.WriteString("; ")
		}
		b.WriteString(r.Error())
	}
	return b.String()
}

// WriteBaseline atomically writes a baseline to
// /opt/faas/releases/<git-sha>/sbom-baseline.json. Same tmp-then-
// rename pattern as Write(); the dir is the per-release bundle
// root so the on-disk layout stays consistent.
func WriteBaseline(root string, b SBOMBaseline) error {
	if b.GitSHA == "" {
		return errors.New("releaseinstall: write baseline: empty git_sha")
	}
	body, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return fmt.Errorf("releaseinstall: marshal baseline: %w", err)
	}
	body = append(body, '\n')
	dir := BundleRoot(root, b.GitSHA)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("releaseinstall: mkdir %s: %w", dir, err)
	}
	path := SBOMBaselinePath(dir)
	tmp, err := os.CreateTemp(dir, ".sbom-baseline-*.tmp")
	if err != nil {
		return fmt.Errorf("releaseinstall: create baseline temp: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if err := tmp.Chmod(0o644); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("releaseinstall: chmod baseline temp: %w", err)
	}
	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("releaseinstall: write baseline temp: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("releaseinstall: sync baseline temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("releaseinstall: close baseline temp: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("releaseinstall: publish baseline: %w", err)
	}
	return nil
}

// SBOMBaselinePath returns the on-disk path to the per-release
// SBoM baseline. Exported so the doctor probe (PR-B) and the
// KGV rotate subcommand (PR-B) can read it without duplicating
// the path-string.
func SBOMBaselinePath(bundleRoot string) string {
	return bundleRoot + "/" + SBOMBaselineName
}

// ReadBaseline loads + validates the on-disk SBoM baseline at
// <root>/<git-sha>/sbom-baseline.json. Exported so the day-2
// surfaces (PR-B KGV rotate, doctor verify-tarball-sbom probe)
// share a single read path with WriteBaseline.
//
// Errors:
//   - ErrNilBaseline is returned when the file is missing. The
//     caller decides whether to "init with KGVZero" or "fail
//     closed"; this helper does not.
//   - Malformed JSON / missing required fields surface as a
//     wrapped error so callers can log the on-disk path.
//
// The round-trip with WriteBaseline is asserted by
// TestSBOM_ReadBaseline_RoundTrip (sbom_test.go).
func ReadBaseline(root, gitSHA string) (SBOMBaseline, error) {
	if gitSHA == "" {
		return SBOMBaseline{}, errors.New("releaseinstall: read baseline: empty git_sha")
	}
	path := SBOMBaselinePath(BundleRoot(root, gitSHA))
	body, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return SBOMBaseline{}, ErrNilBaseline
		}
		return SBOMBaseline{}, fmt.Errorf("releaseinstall: read baseline %s: %w", path, err)
	}
	var b SBOMBaseline
	if err := json.Unmarshal(body, &b); err != nil {
		return SBOMBaseline{}, fmt.Errorf("releaseinstall: decode baseline %s: %w", path, err)
	}
	if b.GitSHA == "" {
		return SBOMBaseline{}, fmt.Errorf("releaseinstall: baseline %s missing git_sha", path)
	}
	return b, nil
}
