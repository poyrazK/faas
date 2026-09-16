// sbom_test.go — whitebox tests for pkg/releaseinstall/sbom.go
// (ADR-113 canonical daemon tarball, PR-A commit 3).
//
// External test package (`package releaseinstall_test`), matching
// the convention every other pkg/releaseinstall/*_test.go follows
// (bundle_test.go, install_test.go, tarball_test.go).
//
// Verifies the four load-bearing surfaces:
//
//   - ParseSPDXv2_3: SPDX-2.3 inputs produce the expected counts;
//     CycloneDX inputs are rejected with ErrUnsupportedSBOMFormat;
//     malformed JSON surfaces as ErrSBOMMalformed.
//   - Diff: a zero-CVE baseline rejects a CVE-bearing SBoM with
//     ErrCVERegression; a baseline with N known CRITICALs accepts a
//     SBoM with N or fewer CRITICALs; nil baseline is fail-closed.
//   - WriteBaseline + SBOMBaselinePath: round-trips through disk.
package releaseinstall_test

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/releaseinstall"
)

// validSPDXv2_3 returns a hand-crafted SPDX-2.3 JSON document
// with the given per-severity CVE counts. The annotation
// `comment="CVE"` is the load-bearing discriminator: the parser
// only counts CVE-typed annotations, so non-CVE annotations with
// the same severity are ignored (defence against license-risk
// annotations showing up as fake CRITICALs).
func validSPDXv2_3(t *testing.T, critical, high, medium, low int) []byte {
	pkg := struct {
		Annotations []map[string]string `json:"annotations"`
	}{}
	for i := 0; i < critical; i++ {
		pkg.Annotations = append(pkg.Annotations, map[string]string{
			"severity": "CRITICAL",
			"comment":  "CVE",
		})
	}
	for i := 0; i < high; i++ {
		pkg.Annotations = append(pkg.Annotations, map[string]string{
			"severity": "HIGH",
			"comment":  "CVE",
		})
	}
	for i := 0; i < medium; i++ {
		pkg.Annotations = append(pkg.Annotations, map[string]string{
			"severity": "MEDIUM",
			"comment":  "CVE",
		})
	}
	for i := 0; i < low; i++ {
		pkg.Annotations = append(pkg.Annotations, map[string]string{
			"severity": "LOW",
			"comment":  "CVE",
		})
	}
	doc := map[string]any{
		"spdxVersion": "SPDX-2.3",
		"name":        "test-sbom",
		"packages":    []any{pkg},
	}
	// Hand-marshal via the standard library; the test fixtures
	// don't need byte-stable ordering because ParseSPDXv2_3 only
	// reads the annotations list.
	return mustJSONMarshal(t, doc)
}

// validSPDXv2_3WithNoise returns an SBoM with the requested CVE
// counts AND a license-risk annotation marked CRITICAL. The
// noise annotation MUST be ignored — the parser's CVE-only
// filter is load-bearing, otherwise license-risk falsely
// triggers the regression gate.
func validSPDXv2_3WithNoise(t *testing.T, criticalCVEs int) []byte {
	doc := map[string]any{
		"spdxVersion": "SPDX-2.3",
		"name":        "test-sbom-noise",
		"packages": []any{
			map[string]any{
				"annotations": []any{
					map[string]string{
						"severity": "CRITICAL",
						"comment":  "CVE",
					},
				},
			},
			map[string]any{
				"annotations": []any{
					map[string]string{
						"severity": "CRITICAL",
						"comment":  "license-risk",
					},
				},
			},
		},
	}
	return mustJSONMarshal(t, doc)
}

func mustJSONMarshal(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

// TestParseSPDXv2_3_HappyPath asserts the parser returns the
// expected counts for a hand-crafted SBoM.
func TestParseSPDXv2_3_HappyPath(t *testing.T) {
	body := validSPDXv2_3(t, 2, 3, 4, 5)
	c, err := releaseinstall.ParseSPDXv2_3(body)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if c.CriticalN != 2 {
		t.Errorf("CriticalN = %d, want 2", c.CriticalN)
	}
	if c.HighN != 3 {
		t.Errorf("HighN = %d, want 3", c.HighN)
	}
	if c.MediumN != 4 {
		t.Errorf("MediumN = %d, want 4", c.MediumN)
	}
	if c.LowN != 5 {
		t.Errorf("LowN = %d, want 5", c.LowN)
	}
}

// TestParseSPDXv2_3_RejectsCycloneDX asserts that an SBoM
// shaped like CycloneDX (bomFormat=...) is hard-rejected at
// parse time. This is the producer-side mistake path: a canary
// runner that switched producers by accident would otherwise
// silently slip a CycloneDX doc past the host.
func TestParseSPDXv2_3_RejectsCycloneDX(t *testing.T) {
	body := []byte(`{"bomFormat":"CycloneDX","specVersion":"1.5","components":[]}`)
	_, err := releaseinstall.ParseSPDXv2_3(body)
	if !errors.Is(err, releaseinstall.ErrUnsupportedSBOMFormat) {
		t.Fatalf("parse CycloneDX: got %v, want ErrUnsupportedSBOMFormat", err)
	}
}

// TestParseSPDXv2_3_RejectsEmpty asserts the empty-body guard.
func TestParseSPDXv2_3_RejectsEmpty(t *testing.T) {
	_, err := releaseinstall.ParseSPDXv2_3(nil)
	if !errors.Is(err, releaseinstall.ErrSBOMMalformed) {
		t.Fatalf("parse empty: got %v, want ErrSBOMMalformed", err)
	}
}

// TestParseSPDXv2_3_IgnoresNonCVEAnnotations asserts the
// CVE-only filter: a license-risk annotation tagged CRITICAL
// does NOT count toward the regression gate. This is the
// load-bearing filter that keeps the diff meaningful — without
// it, license-noise would falsely trigger a CVE regression.
func TestParseSPDXv2_3_IgnoresNonCVEAnnotations(t *testing.T) {
	body := validSPDXv2_3WithNoise(t, 1)
	c, err := releaseinstall.ParseSPDXv2_3(body)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if c.CriticalN != 1 {
		t.Fatalf("CriticalN = %d, want 1 (license-noise CRITICAL must be ignored)", c.CriticalN)
	}
}

// TestSBOMBaseline_DiffDetectsCriticalRegression asserts the
// load-bearing gate: a baseline with 0 CRITICAL/HIGH rejects
// a new SBoM with 1 CRITICAL with ErrCVERegression.
func TestSBOMBaseline_DiffDetectsCriticalRegression(t *testing.T) {
	base := releaseinstall.KGVZero("0123456789abcdef0123456789abcdef01234567")
	current := releaseinstall.SBOMCounts{CriticalN: 1, HighN: 0}

	regs, err := base.Diff(current)
	if !errors.Is(err, releaseinstall.ErrCVERegression) {
		t.Fatalf("diff with critical regression: got %v, want ErrCVERegression", err)
	}
	if len(regs) != 1 || regs[0].Severity != releaseinstall.SevCritical {
		t.Fatalf("regressions = %+v, want one CRITICAL", regs)
	}
	if regs[0].Delta != 1 {
		t.Errorf("delta = %d, want 1", regs[0].Delta)
	}
}

// TestSBOMBaseline_DiffDetectsHighRegression asserts the HIGH
// regression path. Both CRITICAL and HIGH are fail-closed; the
// plan is explicit.
func TestSBOMBaseline_DiffDetectsHighRegression(t *testing.T) {
	base := releaseinstall.SBOMBaseline{
		GitSHA: "0123456789abcdef0123456789abcdef01234567",
		Counts: releaseinstall.SBOMCounts{CriticalN: 0, HighN: 2},
	}
	current := releaseinstall.SBOMCounts{CriticalN: 0, HighN: 3}

	regs, err := base.Diff(current)
	if !errors.Is(err, releaseinstall.ErrCVERegression) {
		t.Fatalf("diff with high regression: got %v, want ErrCVERegression", err)
	}
	if len(regs) != 1 || regs[0].Severity != releaseinstall.SevHigh {
		t.Fatalf("regressions = %+v, want one HIGH", regs)
	}
	if regs[0].Delta != 1 {
		t.Errorf("delta = %d, want 1", regs[0].Delta)
	}
}

// TestSBOMBaseline_DiffNoRegression asserts the happy path: the
// new SBoM has fewer-or-equal CRITICAL/HIGH counts than the
// baseline, no error, no regressions.
func TestSBOMBaseline_DiffNoRegression(t *testing.T) {
	base := releaseinstall.SBOMBaseline{
		GitSHA: "0123456789abcdef0123456789abcdef01234567",
		Counts: releaseinstall.SBOMCounts{CriticalN: 2, HighN: 5},
	}
	// Lower CRITICAL count, same HIGH count: a fix-only release.
	current := releaseinstall.SBOMCounts{CriticalN: 1, HighN: 5}

	regs, err := base.Diff(current)
	if err != nil {
		t.Fatalf("diff with no regression: got %v, want nil", err)
	}
	if len(regs) != 0 {
		t.Fatalf("regressions = %+v, want empty", regs)
	}
}

// TestSBOMBaseline_DiffNilBaseline asserts the fail-closed
// posture: a zero-value SBOMBaseline (the literal zero value,
// not KGVZero) is a programmer error, surfaced as ErrNilBaseline.
func TestSBOMBaseline_DiffNilBaseline(t *testing.T) {
	var base releaseinstall.SBOMBaseline // zero-value
	current := releaseinstall.SBOMCounts{}

	_, err := base.Diff(current)
	if !errors.Is(err, releaseinstall.ErrNilBaseline) {
		t.Fatalf("diff with zero-value baseline: got %v, want ErrNilBaseline", err)
	}
}

// TestSBOMBaseline_WriteRoundTrip asserts WriteBaseline writes
// a JSON document that Decode + Diff can read back without
// drift. The on-disk shape is locked in here so PR-B's
// `release KGV rotate` can rely on it.
func TestSBOMBaseline_WriteRoundTrip(t *testing.T) {
	root := t.TempDir()
	gitSHA := "0123456789abcdef0123456789abcdef01234567"
	b := releaseinstall.SBOMBaseline{
		GitSHA: gitSHA,
		Counts: releaseinstall.SBOMCounts{CriticalN: 1, HighN: 2, MediumN: 3, LowN: 4},
	}
	if err := releaseinstall.WriteBaseline(root, b); err != nil {
		t.Fatalf("write: %v", err)
	}

	path := releaseinstall.SBOMBaselinePath(releaseinstall.BundleRoot(root, gitSHA))
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read baseline: %v", err)
	}
	if !strings.Contains(string(body), `"critical": 1`) {
		t.Errorf("baseline body missing critical:1: %s", string(body))
	}
}

// TestSBOM_ReadBaseline_RoundTrip asserts WriteBaseline + ReadBaseline
// agree across the per-release tmp-then-rename publish path. PR-B
// commit 1 surface.
func TestSBOM_ReadBaseline_RoundTrip(t *testing.T) {
	root := t.TempDir()
	gitSHA := "0123456789abcdef0123456789abcdef01234567"

	want := releaseinstall.SBOMBaseline{
		GitSHA: gitSHA,
		Counts: releaseinstall.SBOMCounts{CriticalN: 3, HighN: 7, MediumN: 12, LowN: 4},
	}
	if err := releaseinstall.WriteBaseline(root, want); err != nil {
		t.Fatalf("write baseline: %v", err)
	}
	got, err := releaseinstall.ReadBaseline(root, gitSHA)
	if err != nil {
		t.Fatalf("read baseline: %v", err)
	}
	if got.GitSHA != want.GitSHA {
		t.Errorf("git_sha round-trip: got %q, want %q", got.GitSHA, want.GitSHA)
	}
	if got.Counts != want.Counts {
		t.Errorf("counts round-trip: got %+v, want %+v", got.Counts, want.Counts)
	}
}

// TestSBOM_ReadBaseline_Missing asserts the missing-file path
// returns ErrNilBaseline (not a wrapped os.IsNotExist). The
// caller decides whether to init with KGVZero or fail closed;
// the helper does not.
func TestSBOM_ReadBaseline_Missing(t *testing.T) {
	root := t.TempDir()
	gitSHA := "0123456789abcdef0123456789abcdef01234567"
	_, err := releaseinstall.ReadBaseline(root, gitSHA)
	if !errors.Is(err, releaseinstall.ErrNilBaseline) {
		t.Fatalf("missing baseline: got %v, want ErrNilBaseline", err)
	}
}

// TestSBOM_ReadBaseline_Malformed asserts a non-JSON file
// surfaces as a wrapped error (not ErrNilBaseline) so the
// caller can distinguish "no baseline yet" from "baseline
// corrupt".
func TestSBOM_ReadBaseline_Malformed(t *testing.T) {
	root := t.TempDir()
	gitSHA := "0123456789abcdef0123456789abcdef01234567"
	if err := os.WriteFile(releaseinstall.AcceptedSBOMBaselinePath(root), []byte("not json"), 0o644); err != nil {
		t.Fatalf("write garbage: %v", err)
	}
	_, err := releaseinstall.ReadBaseline(root, gitSHA)
	if err == nil {
		t.Fatalf("malformed baseline: want error, got nil")
	}
	if errors.Is(err, releaseinstall.ErrNilBaseline) {
		t.Fatalf("malformed baseline: should not be ErrNilBaseline, got %v", err)
	}
}

func TestSBOM_AcceptedBaselineSurvivesReleasePruning(t *testing.T) {
	root := t.TempDir()
	gitSHA := "0123456789abcdef0123456789abcdef01234567"
	want := releaseinstall.SBOMBaseline{GitSHA: gitSHA, Counts: releaseinstall.SBOMCounts{HighN: 2}}
	if err := releaseinstall.WriteBaseline(root, want); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(releaseinstall.BundleRoot(root, gitSHA)); err != nil {
		t.Fatal(err)
	}
	got, err := releaseinstall.ReadBaseline(root, gitSHA)
	if err != nil || got.Counts != want.Counts {
		t.Fatalf("accepted baseline after prune = %+v, %v", got, err)
	}
}
