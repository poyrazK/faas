// Tests for cmd/gregalectl/commands_release_kgv.go.
//
// The flag-validation + dispatch paths are unit-testable without
// any filesystem state. The happy-path + missing-release paths
// require a fixture release dir (manifest + SBoM + on-disk bin),
// which is small enough to inline in the test rather than promote
// to cmd/e2e.
//
// The e2e shard (cmd/e2e/release_kgv_test.go) is the place that
// exercises the round-trip against a real Postgres + a real
// installs-with-tarball flow. The whitebox tests here assert the
// CLI surface, not the cross-tier integration.

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/manifest"
	"github.com/onebox-faas/faas/pkg/releaseinstall"
)

const kgvTestGitSHA = "0123456789abcdef0123456789abcdef01234567"
const kgvIncomingGitSHA = "fedcba9876543210fedcba9876543210fedcba98"

// TestCmdReleaseKGV_Help asserts both `-h` and `--help` route to
// the inner dispatcher and exit 0.
func TestCmdReleaseKGV_Help(t *testing.T) {
	for _, h := range []string{"-h", "--help"} {
		if code := cmdReleaseKGV([]string{h}); code != 0 {
			t.Errorf("cmdReleaseKGV(%s) = %d, want 0", h, code)
		}
	}
}

// TestCmdReleaseKGV_NoArgs asserts the missing-arg path prints
// usage and exits 1 (the parent dispatcher's "no subcommand"
// shape, mirrored here).
func TestCmdReleaseKGV_NoArgs(t *testing.T) {
	if code := cmdReleaseKGV(nil); code != 1 {
		t.Errorf("cmdReleaseKGV(nil) = %d, want 1", code)
	}
}

// TestCmdReleaseKGV_Unknown asserts an unknown leaf exits 2 (the
// "usage error" code, distinct from the no-args exit 1 and from
// the platform/infra exit 3).
func TestCmdReleaseKGV_Unknown(t *testing.T) {
	if code := cmdReleaseKGV([]string{"wat"}); code != 2 {
		t.Errorf("cmdReleaseKGV(wat) = %d, want 2", code)
	}
}

// TestCmdReleaseKGV_InitIsAliasForRotateFromZero pins the alias
// contract: `release kgv init` must (a) exit 0 (NOT 2 like a
// genuine unknown subcommand would), (b) emit the deprecation
// note on stderr, (c) write a KGVZero baseline (the same one
// `release kgv rotate --from-zero` writes). Catches the
// "folded keyword forgotten" regression where the PR-B alias
// path silently starts exiting 2 again.
func TestCmdReleaseKGV_InitIsAliasForRotateFromZero(t *testing.T) {
	root := t.TempDir()
	// Capture stderr to assert the deprecation note surfaces.
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	origStderr := os.Stderr
	os.Stderr = w
	code := cmdReleaseKGV([]string{
		"init",
		"--git-sha=" + kgvTestGitSHA,
		"--releases-root=" + root,
	})
	_ = w.Close()
	os.Stderr = origStderr

	if code != 0 {
		t.Fatalf("cmdReleaseKGV(init) = %d, want 0", code)
	}
	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	got := string(buf[:n])
	if !strings.Contains(got, "alias for 'release kgv rotate --from-zero'") {
		t.Errorf("deprecation note missing on stderr: %q", got)
	}
	// Baseline must be written by the rotate path.
	b, err := releaseinstall.ReadBaseline(root, kgvTestGitSHA)
	if err != nil {
		t.Fatalf("ReadBaseline: %v", err)
	}
	if b.GitSHA != kgvTestGitSHA {
		t.Errorf("baseline.GitSHA = %q, want %q", b.GitSHA, kgvTestGitSHA)
	}
	if b.Counts.CriticalN != 0 || b.Counts.HighN != 0 {
		t.Errorf("baseline counts nonzero (alias path should force --from-zero): %+v", b.Counts)
	}
}

// TestCmdReleaseKGV_HelpLong_OnRotate asserts `--help` on the
// rotate leaf exits 0 (the inner handler short-circuits before
// any flag-parsing).
func TestCmdReleaseKGV_HelpLong_OnRotate(t *testing.T) {
	if code := cmdReleaseKGVRotate([]string{"--help"}); code != 0 {
		t.Errorf("cmdReleaseKGVRotate(--help) = %d, want 0", code)
	}
}

// TestCmdReleaseKGVRotate_MissingFlags asserts the missing-arg
// path exits 1 (usage error).
func TestCmdReleaseKGVRotate_MissingFlags(t *testing.T) {
	if code := cmdReleaseKGVRotate(nil); code != 1 {
		t.Errorf("cmdReleaseKGVRotate(nil) = %d, want 1", code)
	}
}

// TestCmdReleaseKGVRotate_BadGitSHA asserts non-hex / wrong-length
// git-sha is rejected at exit 1 (usage error). The shape check
// runs BEFORE any filesystem access so a malformed CLI argument
// never surfaces as a platform/infra error.
func TestCmdReleaseKGVRotate_BadGitSHA(t *testing.T) {
	for _, sha := range []string{"abc", "0123456789ABCDEF0123456789ABCDEF01234567", "not-a-sha"} {
		if code := cmdReleaseKGVRotate([]string{"--git-sha=" + sha}); code != 1 {
			t.Errorf("cmdReleaseKGVRotate(--git-sha=%q) = %d, want 1 (usage)", sha, code)
		}
	}
}

// TestCmdReleaseKGVRotate_MissingRelease asserts the platform/
// infra path: a valid git-sha but no <releases-root>/<sha>/ on
// disk → exit 3. The error message must name the bundle root so
// the operator can see what to stage.
func TestCmdReleaseKGVRotate_MissingRelease(t *testing.T) {
	root := t.TempDir()
	// Capture stderr to assert the path is in the message.
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	origStderr := os.Stderr
	os.Stderr = w
	defer func() { os.Stderr = origStderr }()
	code := cmdReleaseKGVRotate([]string{
		"--git-sha=" + kgvTestGitSHA,
		"--releases-root=" + root,
	})
	_ = w.Close()
	if code != 3 {
		t.Errorf("cmdReleaseKGVRotate(missing release) = %d, want 3", code)
	}
	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	got := string(buf[:n])
	if !strings.Contains(got, kgvTestGitSHA) {
		t.Errorf("missing-release stderr missing git-sha context: %q", got)
	}
}

// TestCmdReleaseKGVRotate_FromZero asserts the deliberate zero-set
// path. The release dir is NOT required because --from-zero
// skips the SBoM parse. The baseline is written to the canonical
// path with Counts{CriticalN: 0, HighN: 0, ...} and CreatedAt
// "1970-01-01T00:00:00Z".
func TestCmdReleaseKGVRotate_FromZero(t *testing.T) {
	root := t.TempDir()
	if code := cmdReleaseKGVRotate([]string{
		"--git-sha=" + kgvTestGitSHA,
		"--releases-root=" + root,
		"--from-zero",
	}); code != 0 {
		t.Fatalf("cmdReleaseKGVRotate(--from-zero) = %d, want 0", code)
	}
	// Read the baseline back via the pkg/releaseinstall helper
	// (the same path the install-time gate consumes).
	b, err := releaseinstall.ReadBaseline(root, kgvTestGitSHA)
	if err != nil {
		t.Fatalf("ReadBaseline: %v", err)
	}
	if b.GitSHA != kgvTestGitSHA {
		t.Errorf("baseline.GitSHA = %q, want %q", b.GitSHA, kgvTestGitSHA)
	}
	if b.Counts.CriticalN != 0 || b.Counts.HighN != 0 {
		t.Errorf("baseline counts nonzero: %+v", b.Counts)
	}
	if b.CreatedAt != "1970-01-01T00:00:00Z" {
		t.Errorf("baseline CreatedAt = %q, want KGVZero sentinel", b.CreatedAt)
	}
}

// TestCmdReleaseKGVRotate_HappyPath asserts the parse-from-SBoM
// path. Stages a fixture manifest + SBoM on disk, runs the
// command, and reads the baseline back via the package helper.
func TestCmdReleaseKGVRotate_HappyPath(t *testing.T) {
	root := t.TempDir()
	stageFixtureRelease(t, root, kgvTestGitSHA, 2, 3, 4, 5)

	if code := cmdReleaseKGVRotate([]string{
		"--git-sha=" + kgvTestGitSHA,
		"--releases-root=" + root,
	}); code != 0 {
		t.Fatalf("cmdReleaseKGVRotate(happy) = %d, want 0", code)
	}
	b, err := releaseinstall.ReadBaseline(root, kgvTestGitSHA)
	if err != nil {
		t.Fatalf("ReadBaseline: %v", err)
	}
	if b.Counts.CriticalN != 2 || b.Counts.HighN != 3 || b.Counts.MediumN != 4 || b.Counts.LowN != 5 {
		t.Errorf("baseline counts: %+v, want critical:2 high:3 medium:4 low:5", b.Counts)
	}
	if b.CreatedAt == "" || b.CreatedAt == "1970-01-01T00:00:00Z" {
		t.Errorf("baseline CreatedAt = %q, want a real RFC3339 timestamp", b.CreatedAt)
	}
}

// spec: CD must compare an incoming SBOM with the last accepted production
// baseline before activation and leave the current release unchanged when a
// high/critical count regresses.
func TestCmdReleaseKGVVerify_RefusesRegressionBeforeActivation(t *testing.T) {
	root := t.TempDir()
	stageFixtureRelease(t, root, kgvTestGitSHA, 0, 0, 0, 0)
	if code := cmdReleaseKGVRotate([]string{"--git-sha=" + kgvTestGitSHA, "--releases-root=" + root}); code != 0 {
		t.Fatalf("accept baseline: code %d", code)
	}
	if err := releaseinstall.AtomicFlip(root, kgvTestGitSHA); err != nil {
		t.Fatal(err)
	}
	stageFixtureRelease(t, root, kgvIncomingGitSHA, 0, 1, 0, 0)
	if code := cmdReleaseKGVVerify([]string{"--git-sha=" + kgvIncomingGitSHA, "--releases-root=" + root}); code != 3 {
		t.Fatalf("verify regressed release = %d, want 3", code)
	}
	current, err := os.Readlink(releaseinstall.CurrentSymlink(root))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(current, kgvTestGitSHA) {
		t.Fatalf("current changed after refusal: %s", current)
	}
}

func TestCmdReleaseKGVVerify_AcceptsEqualOrLowerCounts(t *testing.T) {
	root := t.TempDir()
	stageFixtureRelease(t, root, kgvTestGitSHA, 1, 2, 0, 0)
	if code := cmdReleaseKGVRotate([]string{"--git-sha=" + kgvTestGitSHA, "--releases-root=" + root}); code != 0 {
		t.Fatalf("accept baseline: code %d", code)
	}
	stageFixtureRelease(t, root, kgvIncomingGitSHA, 1, 1, 0, 0)
	if code := cmdReleaseKGVVerify([]string{"--git-sha=" + kgvIncomingGitSHA, "--releases-root=" + root}); code != 0 {
		t.Fatalf("verify non-regressed release = %d, want 0", code)
	}
}

func TestCmdReleaseKGVVerify_RejectsStaleStampedSBOM(t *testing.T) {
	root := t.TempDir()
	stageFixtureRelease(t, root, kgvIncomingGitSHA, 0, 0, 0, 0)
	if err := releaseinstall.WriteBaseline(root, releaseinstall.KGVZero(kgvTestGitSHA)); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(releaseinstall.BundleRoot(root, kgvIncomingGitSHA), "release.sbom.json")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatal(err)
	}
	doc["documentNamespace"] = "https://gregale.dev/spdxdocs/" + kgvTestGitSHA
	body, err = json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	if code := cmdReleaseKGVVerify([]string{"--git-sha=" + kgvIncomingGitSHA, "--releases-root=" + root}); code != 3 {
		t.Fatalf("verify stale SBoM = %d, want 3", code)
	}
}

// TestCmdReleaseKGVRotate_HappyPath_JSON asserts the --json output
// path emits a parseable JSON document with the expected fields.
// Catches the "struct tags wrong, fail in production" class of
// regressions.
func TestCmdReleaseKGVRotate_HappyPath_JSON(t *testing.T) {
	root := t.TempDir()
	stageFixtureRelease(t, root, kgvTestGitSHA, 1, 0, 0, 0)

	// Pipe stdout to capture the JSON blob.
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	origStdout := os.Stdout
	os.Stdout = w
	code := cmdReleaseKGVRotate([]string{
		"--git-sha=" + kgvTestGitSHA,
		"--releases-root=" + root,
		"--json",
	})
	os.Stdout = origStdout
	_ = w.Close()
	if code != 0 {
		t.Fatalf("cmdReleaseKGVRotate(--json) = %d, want 0", code)
	}
	buf := make([]byte, 8192)
	n, _ := r.Read(buf)
	var got struct {
		GitSHA   string `json:"git_sha"`
		FromZero bool   `json:"from_zero"`
		Path     string `json:"path"`
		Baseline struct {
			GitSHA string `json:"git_sha"`
			Counts struct {
				CriticalN int `json:"critical"`
				HighN     int `json:"high"`
			} `json:"counts"`
		} `json:"baseline"`
	}
	if err := json.Unmarshal(buf[:n], &got); err != nil {
		t.Fatalf("unmarshal stdout: %v (raw: %q)", err, string(buf[:n]))
	}
	if got.GitSHA != kgvTestGitSHA {
		t.Errorf("json git_sha = %q, want %q", got.GitSHA, kgvTestGitSHA)
	}
	if got.FromZero {
		t.Errorf("json from_zero = true, want false")
	}
	if got.Baseline.Counts.CriticalN != 1 {
		t.Errorf("json baseline.counts.critical = %d, want 1", got.Baseline.Counts.CriticalN)
	}
	if got.Path == "" {
		t.Errorf("json path empty")
	}
}

// TestCmdReleaseKGVRotate_MalformedSBOM asserts the unparseable
// SBoM path: a valid git-sha + a fixture dir with a non-JSON
// SBoM file → exit 3 with the offending path named. The legacy
// --from-zero escape hatch is the operator's next step.
func TestCmdReleaseKGVRotate_MalformedSBOM(t *testing.T) {
	root := t.TempDir()
	stageFixtureRelease(t, root, kgvTestGitSHA, 0, 0, 0, 0)
	// Overwrite the SBoM with garbage so the parse step fails.
	dir := releaseinstall.BundleRoot(root, kgvTestGitSHA)
	if err := os.WriteFile(filepath.Join(dir, "release.sbom.json"), []byte("not json"), 0o644); err != nil {
		t.Fatalf("write garbage SBoM: %v", err)
	}
	if code := cmdReleaseKGVRotate([]string{
		"--git-sha=" + kgvTestGitSHA,
		"--releases-root=" + root,
	}); code != 3 {
		t.Errorf("cmdReleaseKGVRotate(malformed SBoM) = %d, want 3", code)
	}
}

// TestCmdReleaseKGVRotate_ManifestGitSHAMismatch asserts the
// producer-side bug path: the on-disk manifest carries a different
// git_sha than --git-sha. The mismatch is a hard error (exit 3);
// the operator should re-stage the release dir with the right
// SHA, not "rotate" into a confused state.
func TestCmdReleaseKGVRotate_ManifestGitSHAMismatch(t *testing.T) {
	root := t.TempDir()
	stageFixtureRelease(t, root, kgvTestGitSHA, 0, 0, 0, 0)
	// Overwrite the manifest IN-PLACE with a different git_sha.
	// releaseinstall.Write would re-route to the new SHA's dir,
	// so we hand-write the bytes directly to the original path.
	dir := releaseinstall.BundleRoot(root, kgvTestGitSHA)
	mismatchedBody := []byte(`{"format_version":"1","git_sha":"ffffffffffffffffffffffffffffffffffffffff","manifest_hash":"sha256:` + strings.Repeat("b", 64) + `","daemon_hashes":{},"created_at":"2026-08-15T00:00:00Z"}`)
	if err := os.WriteFile(filepath.Join(dir, "release-manifest.json"), mismatchedBody, 0o644); err != nil {
		t.Fatalf("write mismatched manifest: %v", err)
	}
	if code := cmdReleaseKGVRotate([]string{
		"--git-sha=" + kgvTestGitSHA,
		"--releases-root=" + root,
	}); code != 3 {
		t.Errorf("cmdReleaseKGVRotate(mismatch) = %d, want 3", code)
	}
}

// stageFixtureRelease writes a minimal release dir
// (<releasesRoot>/<gitSha>/) with a release-manifest.json and an
// SPDX-2.3 SBoM with the requested per-severity CVE counts.
//
// The catalog daemon binaries (9 entries from manifest.HostKeys)
// are stubbed with a single byte so releaseinstall.Build can
// hash them; the hashes themselves are not asserted by the
// downstream tests, only that the manifest validates.
//
// The SBoM body is hand-crafted JSON that ParseSPDXv2_3 accepts
// (`spdxVersion: SPDX-2.3`, no `bomFormat`, `packages[].annotations`
// with `severity` + `comment: "CVE"`).
func stageFixtureRelease(t *testing.T, releasesRoot, gitSHA string, critical, high, medium, low int) {
	t.Helper()
	dir := releaseinstall.BundleRoot(releasesRoot, gitSHA)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	// Stage catalog daemon binaries so Build can hash them.
	bin := releaseinstall.BinDir(releasesRoot, gitSHA)
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	for _, name := range manifest.SortedHostKeys() {
		// Each daemon is a 1-byte stub. The hash is whatever
		// sha256("X") is — the test does not assert the value.
		if err := os.WriteFile(filepath.Join(bin, name), []byte("X"), 0o755); err != nil {
			t.Fatalf("stage %s: %v", name, err)
		}
	}
	// Build the manifest via the production constructor so the
	// DaemonHashes catalog is correct.
	m, err := releaseinstall.Build(releasesRoot, gitSHA, "sha256:"+strings.Repeat("a", 64), time.Now().UTC())
	if err != nil {
		t.Fatalf("build manifest: %v", err)
	}
	if err := releaseinstall.Write(releasesRoot, m); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	// Build the SBoM body. Annotations list, one per CVE.
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
	if err := os.WriteFile(filepath.Join(dir, "release.sbom.json"), body, 0o644); err != nil {
		t.Fatalf("write SBoM: %v", err)
	}
}
