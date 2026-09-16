// commands_doctor_sbom_test.go — focused tests for the doctor's
// verify-tarball-sbom probe (PR-B / ADR-113 day-2).
//
// Verifies the on-disk-shape → finding mapping:
//
//   - missing triple        → warn (legacy operators continue)
//   - happy path            → ok (signature identity + counts line)
//   - missing SBoM baseline → warn (operator prompt to rotate KGV)
//
// The probe's verifier is swapped via deps.verifier to a
// FixtureCosignVerifier so the test doesn't shell out to cosign.
// The fixture verifies the call shape (tarball path + sig path
// were passed correctly) and returns a stable identity string
// for the OK-finding assertion.

package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/manifest"
	"github.com/onebox-faas/faas/pkg/releaseinstall"
)

const sbomTestGitSHA = "0123456789abcdef0123456789abcdef01234567"

// sbomStageTriple stages the canonical on-disk triple at
// <releasesRoot>/<gitSHA>/{release.tar.gz,release.cosign.bundle,
// release.sbom.json,release-manifest.json,sbom-baseline.json}.
// Returns the bundle root so the caller can inject errors by
// deleting individual files.
//
// The tarball is built via releaseinstall.BuildTarball so its
// hashWalk matches the manifest.BinSHA256 the verifier expects.
// The baseline is written from `baseline` (KGVZero for the
// happy path; the test that exercises the missing-baseline warn
// skips writing sbom-baseline.json).
func sbomStageTriple(t *testing.T, releasesRoot, gitSHA string, baseline *releaseinstall.SBOMBaseline, critical, high, medium, low int) {
	t.Helper()
	dir := releaseinstall.BundleRoot(releasesRoot, gitSHA)
	bin := releaseinstall.BinDir(releasesRoot, gitSHA)
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	now := time.Now().UTC()
	// Stage catalog daemon binaries so the manifest + tarball
	// hash walk matches the verifier's expectations.
	for _, name := range manifest.SortedHostKeys() {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("X"), 0o755); err != nil {
			t.Fatalf("stage %s: %v", name, err)
		}
	}
	m, err := releaseinstall.Build(releasesRoot, gitSHA, "sha256:"+strings.Repeat("a", 64), now)
	if err != nil {
		t.Fatalf("build manifest: %v", err)
	}
	if err := releaseinstall.Write(releasesRoot, m); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	tb, err := releaseinstall.BuildTarball(releasesRoot, gitSHA, m.ManifestHash, now)
	if err != nil {
		t.Fatalf("build tarball: %v", err)
	}
	// BuildTarball canonicalises the manifest timestamp because the
	// manifest is signed inside the reproducible tarball. Stage that same
	// canonical manifest on disk so the doctor reconstructs the exact
	// producer output.
	if err := releaseinstall.Write(releasesRoot, tb.Manifest); err != nil {
		t.Fatalf("write canonical manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "release.tar.gz"), tb.Packed, 0o644); err != nil {
		t.Fatalf("write tarball: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "release.cosign.bundle"), []byte(`{"payload":"fake"}`), 0o644); err != nil {
		t.Fatalf("write cosign bundle: %v", err)
	}
	sbomBody := sbomTestBody(t, critical, high, medium, low)
	if err := os.WriteFile(filepath.Join(dir, "release.sbom.json"), sbomBody, 0o644); err != nil {
		t.Fatalf("write SBoM: %v", err)
	}
	if baseline != nil {
		if err := releaseinstall.WriteBaseline(releasesRoot, *baseline); err != nil {
			t.Fatalf("write baseline: %v", err)
		}
	}
}

// sbomTestBody returns a hand-crafted SPDX-2.3 SBoM with the
// requested per-severity CVE counts. Mirrors the parser's
// accepted shape (no bomFormat, annotations with comment=CVE).
func sbomTestBody(t *testing.T, critical, high, medium, low int) []byte {
	t.Helper()
	type annotation struct {
		Severity string `json:"severity"`
		Comment  string `json:"comment"`
	}
	type pkg struct {
		Annotations []annotation `json:"annotations"`
	}
	type sbom struct {
		SPDXVersion string `json:"spdxVersion"`
		Name        string `json:"name"`
		Packages    []pkg  `json:"packages"`
	}
	var p pkg
	for i := 0; i < critical; i++ {
		p.Annotations = append(p.Annotations, annotation{Severity: "CRITICAL", Comment: "CVE"})
	}
	for i := 0; i < high; i++ {
		p.Annotations = append(p.Annotations, annotation{Severity: "HIGH", Comment: "CVE"})
	}
	for i := 0; i < medium; i++ {
		p.Annotations = append(p.Annotations, annotation{Severity: "MEDIUM", Comment: "CVE"})
	}
	for i := 0; i < low; i++ {
		p.Annotations = append(p.Annotations, annotation{Severity: "LOW", Comment: "CVE"})
	}
	body, err := json.Marshal(sbom{
		SPDXVersion: "SPDX-2.3",
		Name:        "test-sbom",
		Packages:    []pkg{p},
	})
	if err != nil {
		t.Fatalf("marshal SBoM: %v", err)
	}
	return body
}

// TestCheckVerifyTarballSBOM_NoReleases asserts the empty-state
// finding: a releases root that has no SHA-named subdirs emits a
// single OK finding ("no releases on disk") so the probe is
// always present in the JSON report.
func TestCheckVerifyTarballSBOM_NoReleases(t *testing.T) {
	root := t.TempDir()
	deps := &doctorDeps{
		releasesRoot: root,
		verifier:     &releaseinstall.FixtureCosignVerifier{Identity: "irrelevant"},
	}
	findings, err := checkVerifyTarballSBOM(context.Background(), deps)
	if err != nil {
		t.Fatalf("checkVerifyTarballSBOM: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("findings = %d, want 1", len(findings))
	}
	if findings[0].Severity != doctorSeverityOK {
		t.Errorf("severity = %q, want ok", findings[0].Severity)
	}
}

// TestCheckVerifyTarballSBOM_MissingTriple asserts the legacy-
// operator path: a release dir with just bin/+manifest (no
// triple) emits a single warn finding per release.
func TestCheckVerifyTarballSBOM_MissingTriple(t *testing.T) {
	root := t.TempDir()
	// Stage a release dir with bin + manifest but NO triple.
	bin := releaseinstall.BinDir(root, sbomTestGitSHA)
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	for _, name := range manifest.SortedHostKeys() {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("X"), 0o755); err != nil {
			t.Fatalf("stage %s: %v", name, err)
		}
	}
	m, err := releaseinstall.Build(root, sbomTestGitSHA, "sha256:"+strings.Repeat("a", 64), time.Now().UTC())
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if err := releaseinstall.Write(root, m); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	deps := &doctorDeps{
		releasesRoot: root,
		verifier:     &releaseinstall.FixtureCosignVerifier{Identity: "irrelevant"},
	}
	findings, err := checkVerifyTarballSBOM(context.Background(), deps)
	if err != nil {
		t.Fatalf("checkVerifyTarballSBOM: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("findings = %d, want 1", len(findings))
	}
	if findings[0].Severity != doctorSeverityWarn {
		t.Errorf("severity = %q, want warn", findings[0].Severity)
	}
	if findings[0].Target != sbomTestGitSHA {
		t.Errorf("target = %q, want %q", findings[0].Target, sbomTestGitSHA)
	}
}

// TestCheckVerifyTarballSBOM_HappyPath asserts the canonical
// path: triple + baseline + OK SBoM counts → OK finding with
// signature identity + counts line + the verifier was called
// with the correct args.
func TestCheckVerifyTarballSBOM_HappyPath(t *testing.T) {
	root := t.TempDir()
	// 0 CVEs in the SBoM, KGVZero baseline — no regression.
	baseline := releaseinstall.KGVZero(sbomTestGitSHA)
	sbomStageTriple(t, root, sbomTestGitSHA, &baseline, 0, 0, 0, 0)

	verifier := &releaseinstall.FixtureCosignVerifier{Identity: "test-identity@github.com"}
	deps := &doctorDeps{
		releasesRoot: root,
		verifier:     verifier,
	}
	findings, err := checkVerifyTarballSBOM(context.Background(), deps)
	if err != nil {
		t.Fatalf("checkVerifyTarballSBOM: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("findings = %d, want 1", len(findings))
	}
	if findings[0].Severity != doctorSeverityOK {
		t.Errorf("severity = %q, want ok", findings[0].Severity)
	}
	if !strings.Contains(findings[0].Message, "signature=test-identity@github.com") {
		t.Errorf("message missing signature identity: %q", findings[0].Message)
	}
	if !strings.Contains(findings[0].Message, "baseline=critical:0 high:0 medium:0 low:0 live=critical:0 high:0 medium:0 low:0") {
		t.Errorf("message missing baseline/live counts side-by-side: %q", findings[0].Message)
	}
	if verifier.CallCount != 1 {
		t.Errorf("verifier call count = %d, want 1", verifier.CallCount)
	}
}

// TestCheckVerifyTarballSBOM_MissingBaseline asserts the warn-
// finding path: a release with the triple but no baseline emits
// a warn that names the KGV rotate command (so the operator can
// copy-paste it).
func TestCheckVerifyTarballSBOM_MissingBaseline(t *testing.T) {
	root := t.TempDir()
	// No baseline (nil argument).
	sbomStageTriple(t, root, sbomTestGitSHA, nil, 0, 0, 0, 0)

	deps := &doctorDeps{
		releasesRoot: root,
		verifier:     &releaseinstall.FixtureCosignVerifier{Identity: "test-identity"},
	}
	findings, err := checkVerifyTarballSBOM(context.Background(), deps)
	if err != nil {
		t.Fatalf("checkVerifyTarballSBOM: %v", err)
	}
	// Find the missing-baseline warn among the findings. The probe
	// may also emit an OK if the verifying path succeeded; we only
	// assert the missing-baseline path is surfaced.
	var sawMissingBaseline bool
	for _, f := range findings {
		if f.Severity == doctorSeverityWarn && strings.Contains(f.Message, "release kgv rotate") {
			sawMissingBaseline = true
		}
	}
	if !sawMissingBaseline {
		t.Errorf("missing-baseline warn not surfaced; findings=%+v", findings)
	}
}

// TestCheckVerifyTarballSBOM_Regression asserts the fail-closed
// path: a release with the triple + a baseline that does NOT
// cover the new SBoM's CRITICAL count → error finding with the
// regression detail.
func TestCheckVerifyTarballSBOM_Regression(t *testing.T) {
	root := t.TempDir()
	// Baseline is KGVZero (zero CRITICAL); the SBoM has 1 CRITICAL.
	baseline := releaseinstall.KGVZero(sbomTestGitSHA)
	sbomStageTriple(t, root, sbomTestGitSHA, &baseline, 1, 0, 0, 0)

	deps := &doctorDeps{
		releasesRoot: root,
		verifier:     &releaseinstall.FixtureCosignVerifier{Identity: "test-identity"},
	}
	findings, err := checkVerifyTarballSBOM(context.Background(), deps)
	if err != nil {
		t.Fatalf("checkVerifyTarballSBOM: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("findings = %d, want 1", len(findings))
	}
	if findings[0].Severity != doctorSeverityError {
		t.Errorf("severity = %q, want error", findings[0].Severity)
	}
	if !strings.Contains(findings[0].Message, "SBoM CVE regression") {
		t.Errorf("message missing regression marker: %q", findings[0].Message)
	}
}

func TestCheckVerifyTarballSBOM_DoesNotCompareInactiveReleaseToCurrentBaseline(t *testing.T) {
	root := t.TempDir()
	oldSHA := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	newSHA := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	sbomStageTriple(t, root, oldSHA, nil, 1, 0, 0, 0)
	baseline := releaseinstall.KGVZero(newSHA)
	sbomStageTriple(t, root, newSHA, &baseline, 0, 0, 0, 0)
	if err := releaseinstall.AtomicFlip(root, newSHA); err != nil {
		t.Fatal(err)
	}

	findings, err := checkVerifyTarballSBOM(context.Background(), &doctorDeps{
		releasesRoot: root,
		verifier:     &releaseinstall.FixtureCosignVerifier{Identity: "test-identity"},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, finding := range findings {
		if finding.Target == oldSHA && finding.Severity != doctorSeverityOK {
			t.Fatalf("inactive release compared with active baseline: %+v", finding)
		}
	}
}

func TestCheckVerifyTarballSBOM_RejectsUnacceptedActiveRelease(t *testing.T) {
	root := t.TempDir()
	oldSHA := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	newSHA := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	baseline := releaseinstall.KGVZero(oldSHA)
	sbomStageTriple(t, root, oldSHA, &baseline, 0, 0, 0, 0)
	sbomStageTriple(t, root, newSHA, nil, 0, 0, 0, 0)
	if err := releaseinstall.AtomicFlip(root, newSHA); err != nil {
		t.Fatal(err)
	}

	findings, err := checkVerifyTarballSBOM(context.Background(), &doctorDeps{
		releasesRoot: root,
		verifier:     &releaseinstall.FixtureCosignVerifier{Identity: "test-identity"},
	})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, finding := range findings {
		if finding.Target == newSHA && finding.Severity == doctorSeverityError && strings.Contains(finding.Message, "not accepted") {
			found = true
		}
	}
	if !found {
		t.Fatalf("unaccepted active release was not rejected: %+v", findings)
	}
}

// TestCheckVerifyTarballSBOM_VerifyFailed asserts the cosign
// half: a release with the triple but a verifier that returns
// an error → error finding with the cosign error in the Detail.
func TestCheckVerifyTarballSBOM_VerifyFailed(t *testing.T) {
	root := t.TempDir()
	baseline := releaseinstall.KGVZero(sbomTestGitSHA)
	sbomStageTriple(t, root, sbomTestGitSHA, &baseline, 0, 0, 0, 0)

	verifier := &releaseinstall.FixtureCosignVerifier{
		Identity: "should-not-be-used",
		Err:      errCosignFixture("simulated verify-blob failure"),
	}
	deps := &doctorDeps{
		releasesRoot: root,
		verifier:     verifier,
	}
	findings, err := checkVerifyTarballSBOM(context.Background(), deps)
	if err != nil {
		t.Fatalf("checkVerifyTarballSBOM: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("findings = %d, want 1", len(findings))
	}
	if findings[0].Severity != doctorSeverityError {
		t.Errorf("severity = %q, want error", findings[0].Severity)
	}
	if !strings.Contains(findings[0].Message, "tarball verify failed") {
		t.Errorf("message missing verify-failed marker: %q", findings[0].Message)
	}
}

// errCosignFixture returns a stable error for the fixture
// verifier. Avoids depending on the package's own sentinel
// error so the test stays in `package main`.
func errCosignFixture(s string) error { return &fixtureErr{msg: s} }

type fixtureErr struct{ msg string }

func (e *fixtureErr) Error() string { return e.msg }
