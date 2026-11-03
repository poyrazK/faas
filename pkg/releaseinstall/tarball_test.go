// tarball_test.go — whitebox tests for pkg/releaseinstall/tarball.go
// (ADR-113 canonical daemon tarball, PR-A commit 1).
//
// External test package, matching the convention
// pkg/releaseinstall/bundle_test.go and pkg/releaseinstall/install_test.go
// follow (one test file per source file, package _test). The
// tests here all use the existing `pkg/releaseinstall.Build` and
// the catalog from `pkg/manifest.SortedHostKeys()` — no new
// dependencies are pulled in.
//
// Verifies:
//   - BuildTarball produces BYTE-IDENTICAL Packed bytes across
//     rebuilds (TestTarball_Build_HashStable).
//   - Tarball.Extract writes the same bytes the manifest advertises
//     (TestTarball_Extract_RoundTrip).
//   - Extract is idempotent: a second extract on top of the first
//     produces the same file bytes (TestTarball_Extract_Idempotent).
//   - Verify fails closed on a tampered Manifest (the cosign wrapper
//     in commit 2 will be the fail-closed cosign path;
//     TestTarball_Verify_RejectsTampered covers the manifest half
//     here, with the SIG path added in commit 2).
package releaseinstall_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/manifest"
	"github.com/onebox-faas/faas/pkg/releaseinstall"
)

// fakeBinDir creates a temporary bin dir containing one file per
// daemon in the canonical catalog. Each file's bytes are
// deterministic (the daemon name repeated until the byte quota is
// met) so two builds against the same bin dir produce identical
// tarballs.
//
// Returned is the bin dir path + a cleanup func the caller defers.
func fakeBinDir(t *testing.T) (binDir, releasesRoot, gitSHA, manifestHash string) {
	t.Helper()
	root := t.TempDir()
	gitSHA = "0123456789abcdef0123456789abcdef01234567"
	manifestHash = "sha256:" + strings.Repeat("a", 64)
	binDir = filepath.Join(root, gitSHA, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	// Use the same catalog as pkg/releaseinstall.Build so a future
	// PR that adds a daemon automatically grows the test.
	for _, name := range manifest.SortedHostKeys() {
		if err := os.WriteFile(filepath.Join(binDir, name), deterministicBody(name), 0o755); err != nil {
			t.Fatalf("write daemon %s: %v", name, err)
		}
	}
	for _, name := range releaseinstall.SupportBinaryNames() {
		if err := os.WriteFile(filepath.Join(binDir, name), deterministicBody(name), 0o755); err != nil {
			t.Fatalf("write support binary %s: %v", name, err)
		}
	}
	return binDir, root, gitSHA, manifestHash
}

// signedTarball is the helper every happy-path test uses: it
// builds a Tarball and stamps a fake cosign signature onto it so
// Tarball.Verify's cosign half has something to verify against
// the fixture. Commit 2's load-bearing seam: BuildTarball alone
// leaves Sig empty; the fixture verifier is what allows the
// happy-path tests to assert on the identity-stamping side
// without a live cosign binary.
//
// Tests that want to assert the *negative* path (sig missing,
// tampered tarball, etc.) skip this helper and mutate the
// returned Tarball directly.
func signedTarball(t *testing.T) (*releaseinstall.Tarball, string) {
	t.Helper()
	_, root, gitSHA, manifestHash := fakeBinDir(t)
	tb, err := releaseinstall.BuildTarball(root, gitSHA, manifestHash, time.Now().UTC())
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	const identity = "https://github.com/poyrazK/faas/.github/workflows/release.yml@refs/tags/v1.2.3"
	tb.Sig = []byte("fake-cosign-bundle-for-" + identity)
	return tb, identity
}

// catalogDaemons is the in-test alias for the canonical daemon
// catalog. Tests use this to enumerate the set without
// hard-coding names.
func catalogDaemons() []string {
	return manifest.SortedHostKeys()
}

// deterministicBody returns a daemon-name-driven byte slice. The
// byte length is 1024 so sha256 is non-trivial but the test still
// runs in milliseconds.
func deterministicBody(name string) []byte {
	const size = 1024
	out := make([]byte, size)
	for i := range out {
		out[i] = name[i%len(name)]
	}
	return out
}

// TestTarball_Build_HashStable asserts BuildTarball produces the same
// bytes when called twice on the same bin dir (with a different
// `now` value — the load-bearing test that Build doesn't accidentally
// embed a wall-clock value in the tarball).
func TestTarball_Build_HashStable(t *testing.T) {
	_, root, gitSHA, manifestHash := fakeBinDir(t)

	t1, err := releaseinstall.BuildTarball(root, gitSHA, manifestHash, time.Unix(1_700_000_000, 0).UTC())
	if err != nil {
		t.Fatalf("build #1: %v", err)
	}
	t2, err := releaseinstall.BuildTarball(root, gitSHA, manifestHash, time.Unix(1_800_000_000, 0).UTC())
	if err != nil {
		t.Fatalf("build #2: %v", err)
	}

	if len(t1.Packed) != len(t2.Packed) {
		t.Fatalf("packed length differs: %d vs %d (timestamps leaked into the tarball)", len(t1.Packed), len(t2.Packed))
	}
	if !bytes.Equal(t1.Packed, t2.Packed) {
		h1 := sha256.Sum256(t1.Packed)
		h2 := sha256.Sum256(t2.Packed)
		t.Fatalf("packed bytes differ:\n  build #1: %s\n  build #2: %s",
			hex.EncodeToString(h1[:]), hex.EncodeToString(h2[:]))
	}
	// The canonical artifact deliberately pins CreatedAt so the embedded
	// manifest participates in byte-stable reproducible builds.
	if !t1.Manifest.CreatedAt.Equal(t2.Manifest.CreatedAt) {
		t.Fatalf("canonical manifest CreatedAt differs between builds")
	}
}

func TestTarball_Build_EmbedsManifest(t *testing.T) {
	_, root, gitSHA, manifestHash := fakeBinDir(t)
	tb, err := releaseinstall.BuildTarball(root, gitSHA, manifestHash, time.Unix(1_700_000_000, 0).UTC())
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	gz, err := gzip.NewReader(bytes.NewReader(tb.Packed))
	if err != nil {
		t.Fatalf("gzip reader: %v", err)
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	var manifestBody []byte
	for {
		hdr, nextErr := tr.Next()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			t.Fatalf("tar next: %v", nextErr)
		}
		if hdr.Name == releaseinstall.ManifestName {
			manifestBody, err = io.ReadAll(tr)
			if err != nil {
				t.Fatalf("read manifest: %v", err)
			}
			break
		}
	}
	if len(manifestBody) == 0 {
		t.Fatalf("tarball does not contain %s", releaseinstall.ManifestName)
	}
	var embedded releaseinstall.Manifest
	if err := json.Unmarshal(manifestBody, &embedded); err != nil {
		t.Fatalf("decode embedded manifest: %v", err)
	}
	if embedded.GitSHA != gitSHA || embedded.ManifestHash != manifestHash {
		t.Fatalf("embedded identity = (%s, %s), want (%s, %s)", embedded.GitSHA, embedded.ManifestHash, gitSHA, manifestHash)
	}
}

func TestTarball_Build_IncludesScheddBrokerQHelper(t *testing.T) {
	_, root, gitSHA, manifestHash := fakeBinDir(t)
	tb, err := releaseinstall.BuildTarball(root, gitSHA, manifestHash, time.Unix(1_700_000_000, 0).UTC())
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	if _, ok := tb.Manifest.ToolHashes["schedd-brokerq-apply"]; !ok {
		t.Fatalf("manifest tool_hashes does not include schedd-brokerq-apply: %v", tb.Manifest.ToolHashes)
	}

	gz, err := gzip.NewReader(bytes.NewReader(tb.Packed))
	if err != nil {
		t.Fatalf("gzip reader: %v", err)
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	for {
		hdr, nextErr := tr.Next()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			t.Fatalf("tar next: %v", nextErr)
		}
		if hdr.Name == "schedd-brokerq-apply" {
			return
		}
	}
	t.Fatal("canonical tarball does not contain schedd-brokerq-apply")
}

func TestTarball_LegacyUnhashedGuestInitIsAccepted(t *testing.T) {
	binDir, root, gitSHA, manifestHash := fakeBinDir(t)
	if err := os.WriteFile(filepath.Join(binDir, "init"), deterministicBody("init"), 0o755); err != nil {
		t.Fatalf("write init: %v", err)
	}
	tb, err := releaseinstall.BuildTarball(root, gitSHA, manifestHash, time.Unix(1_700_000_000, 0).UTC())
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	// This models a signed pre-canonical artifact whose archive contains
	// guest-init but whose manifest predates the tool_hashes entry.
	delete(tb.Manifest.ToolHashes, "init")
	delete(tb.ToolSHA256, "init")
	extractRoot := t.TempDir()
	if err := tb.Extract(extractRoot); err != nil {
		t.Fatalf("extract legacy init: %v", err)
	}
	if _, err := os.Stat(filepath.Join(extractRoot, gitSHA, "bin", "init")); err != nil {
		t.Fatalf("extracted legacy init: %v", err)
	}
}

// TestTarball_Verify_BlessedManifests asserts Verify returns nil
// on a tarball that was just produced and stamped with a fake cosign
// signature (the happy path — no tampering, the fixture verifier
// says "yes, the OIDC identity matches"). Also asserts the cert
// identity got stamped onto Manifest.Signature so audit trails see
// who signed it.
func TestTarball_Verify_BlessedManifests(t *testing.T) {
	tb, wantIdentity := signedTarball(t)
	verifier := &releaseinstall.FixtureCosignVerifier{Identity: wantIdentity}

	gotIdentity, err := tb.Verify(context.Background(), verifier)
	if err != nil {
		t.Fatalf("verify blessed tarball: %v", err)
	}
	if gotIdentity != wantIdentity {
		t.Fatalf("verify blessed tarball: identity = %q, want %q", gotIdentity, wantIdentity)
	}
	if tb.Manifest.Signature != wantIdentity {
		t.Fatalf("verify blessed tarball: manifest.Signature = %q, want %q", tb.Manifest.Signature, wantIdentity)
	}
}

// TestTarball_Verify_RejectsTampered asserts that bit-flipping the
// tarball bytes makes Verify fail closed with ErrTarballTampered.
// The cosign half runs first in the Verify pipeline only AFTER the
// hash-walk half; a tampered Packed will fail hashWalk before the
// fixture verifier is consulted. Commit 2's load-bearing test: even
// if the cosign path were "yes" (the fixture), the hash half refuses
// the tarball — defence-in-depth.
func TestTarball_Verify_RejectsTampered(t *testing.T) {
	tb, identity := signedTarball(t)
	// Flip a bit in the middle of the gz payload.
	tampered := append([]byte(nil), tb.Packed...)
	tampered[len(tampered)/2] ^= 0x01
	tb2 := *tb
	tb2.Packed = tampered

	verifier := &releaseinstall.FixtureCosignVerifier{Identity: identity}
	_, err := tb2.Verify(context.Background(), verifier)
	if err == nil {
		t.Fatalf("verify on tampered tarball: want error, got nil")
	}
	if !errors.Is(err, releaseinstall.ErrTarballTampered) {
		t.Fatalf("verify on tampered tarball: got %v, want ErrTarballTampered", err)
	}
}

// TestTarball_Verify_RejectsMissingSig asserts that a Tarball with
// an empty Sig field fails closed with ErrCosignSigMissing — even
// if the hash walk would otherwise pass. This is the trust-bit
// invariant: a tarball without a sig is NOT a canonical tarball.
func TestTarball_Verify_RejectsMissingSig(t *testing.T) {
	tb, identity := signedTarball(t)
	tb.Sig = nil

	verifier := &releaseinstall.FixtureCosignVerifier{Identity: identity}
	_, err := tb.Verify(context.Background(), verifier)
	if !errors.Is(err, releaseinstall.ErrCosignSigMissing) {
		t.Fatalf("verify on unsigned tarball: got %v, want ErrCosignSigMissing", err)
	}
}

// TestTarball_Verify_NilCosignVerifier asserts the commit-2
// invariant: a nil verifier is a programmer error, surfaced as a
// non-nil error (not a panic on a nil-interface call).
func TestTarball_Verify_NilCosignVerifier(t *testing.T) {
	tb, _ := signedTarball(t)
	_, err := tb.Verify(context.Background(), nil)
	if err == nil {
		t.Fatalf("verify with nil verifier: want error, got nil")
	}
	if errors.Is(err, releaseinstall.ErrTarballTampered) {
		t.Fatalf("verify with nil verifier: should not be ErrTarballTampered, got %v", err)
	}
}

// TestTarball_Verify_PropagatesVerifierErr asserts that when the
// fixture verifier returns an error, Verify wraps and surfaces it
// (not as ErrTarballTampered — the trust-bit path has its own
// error class).
func TestTarball_Verify_PropagatesVerifierErr(t *testing.T) {
	tb, _ := signedTarball(t)
	sentinel := errors.New("fixture: cert expired")
	verifier := &releaseinstall.FixtureCosignVerifier{Err: sentinel}

	_, err := tb.Verify(context.Background(), verifier)
	if !errors.Is(err, sentinel) {
		t.Fatalf("verify with failing verifier: got %v, want wraps %v", err, sentinel)
	}
}

// TestTarball_Extract_RoundTrip builds, extracts, then re-Verifies
// the on-disk tree via the existing pkg/releaseinstall.Verify —
// proving that the tarball round-trips through extraction without
// any byte drift on the catalog binaries.
func TestTarball_Extract_RoundTrip(t *testing.T) {
	bin, root, gitSHA, manifestHash := fakeBinDir(t)
	tb, err := releaseinstall.BuildTarball(root, gitSHA, manifestHash, time.Now().UTC())
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	// Clear bin/ and re-extract so we know the on-disk bytes
	// come ONLY from the tarball.
	for _, name := range catalogDaemons() {
		if err := os.Remove(filepath.Join(bin, name)); err != nil && !os.IsNotExist(err) {
			t.Fatalf("clear %s: %v", name, err)
		}
	}
	if err := tb.Extract(root); err != nil {
		t.Fatalf("extract: %v", err)
	}
	if err := releaseinstall.Verify(root, tb.Manifest); err != nil {
		t.Fatalf("on-disk verify after extract: %v", err)
	}
}

// TestTarball_Extract_Idempotent extracts twice and asserts the
// on-disk bytes are identical.
func TestTarball_Extract_Idempotent(t *testing.T) {
	bin, root, gitSHA, manifestHash := fakeBinDir(t)
	tb, err := releaseinstall.BuildTarball(root, gitSHA, manifestHash, time.Now().UTC())
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if err := tb.Extract(root); err != nil {
		t.Fatalf("extract #1: %v", err)
	}
	daemons := catalogDaemons()
	hashes1 := make(map[string]string, len(daemons))
	for _, n := range daemons {
		body, err := os.ReadFile(filepath.Join(bin, n))
		if err != nil {
			t.Fatalf("read %s: %v", n, err)
		}
		h := sha256.Sum256(body)
		hashes1[n] = hex.EncodeToString(h[:])
	}

	if err := tb.Extract(root); err != nil {
		t.Fatalf("extract #2: %v", err)
	}
	for _, n := range daemons {
		body, err := os.ReadFile(filepath.Join(bin, n))
		if err != nil {
			t.Fatalf("read %s: %v", n, err)
		}
		h := sha256.Sum256(body)
		got := hex.EncodeToString(h[:])
		if got != hashes1[n] {
			t.Fatalf("daemon %s idempotency: extract #2 sha256=%s, want %s", n, got, hashes1[n])
		}
	}
}

// v0.1.18-rc.312: a no-drain rollout rerun re-installed fsn-3's active release
// while its daemons ran from it, and writing through a running binary failed
// with ETXTBSY. Extract must replace each file (a new inode at the path), so
// a process keeps the one it is executing, and leave no temp files behind.
func TestTarball_Extract_ReplacesFilesInsteadOfWritingThroughThem(t *testing.T) {
	bin, root, gitSHA, manifestHash := fakeBinDir(t)
	tb, err := releaseinstall.BuildTarball(root, gitSHA, manifestHash, time.Now().UTC())
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if err := tb.Extract(root); err != nil {
		t.Fatalf("extract #1: %v", err)
	}
	before := make(map[string]os.FileInfo)
	for _, n := range catalogDaemons() {
		info, err := os.Stat(filepath.Join(bin, n))
		if err != nil {
			t.Fatalf("stat %s: %v", n, err)
		}
		before[n] = info
	}
	if err := tb.Extract(root); err != nil {
		t.Fatalf("extract #2: %v", err)
	}
	for _, n := range catalogDaemons() {
		after, err := os.Stat(filepath.Join(bin, n))
		if err != nil {
			t.Fatalf("stat %s: %v", n, err)
		}
		if os.SameFile(before[n], after) {
			t.Fatalf("daemon %s was written through in place; want a replaced file", n)
		}
		if after.Mode().Perm() != 0o755 {
			t.Fatalf("daemon %s mode = %v, want 0755", n, after.Mode().Perm())
		}
	}
	entries, err := os.ReadDir(bin)
	if err != nil {
		t.Fatalf("read bin: %v", err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Fatalf("extract left temp file %s", e.Name())
		}
	}
	if err := releaseinstall.Verify(root, tb.Manifest); err != nil {
		t.Fatalf("on-disk verify after re-extract: %v", err)
	}
}

func TestTarball_Extract_PreservesRuntimeAssetPaths(t *testing.T) {
	bin, root, gitSHA, manifestHash := fakeBinDir(t)
	for _, name := range releaseinstall.RuntimeAssetNames() {
		path := filepath.Join(bin, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir runtime asset %s: %v", name, err)
		}
		if err := os.WriteFile(path, deterministicBody(name), 0o755); err != nil {
			t.Fatalf("write runtime asset %s: %v", name, err)
		}
	}
	tb, err := releaseinstall.BuildTarball(root, gitSHA, manifestHash, time.Now().UTC())
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	for _, name := range catalogDaemons() {
		if err := os.Remove(filepath.Join(bin, name)); err != nil {
			t.Fatalf("clear daemon %s: %v", name, err)
		}
	}
	if err := os.RemoveAll(filepath.Join(bin, "runners")); err != nil {
		t.Fatalf("clear runners: %v", err)
	}
	if err := tb.Extract(root); err != nil {
		t.Fatalf("extract: %v", err)
	}
	if err := releaseinstall.Verify(root, tb.Manifest); err != nil {
		t.Fatalf("verify after extract: %v", err)
	}
	for _, name := range releaseinstall.RuntimeAssetNames() {
		got, err := os.ReadFile(filepath.Join(bin, filepath.FromSlash(name)))
		if err != nil {
			t.Fatalf("read runtime asset %s: %v", name, err)
		}
		if string(got) != string(deterministicBody(name)) {
			t.Fatalf("runtime asset %s changed during extract", name)
		}
	}
}

// TestTarball_Extract_RefusesNilTarball defends against the trivial
// misuse path (pointer-deref panics) — Verify / Extract are
// public methods; a nil receiver is the most common mistake callers
// make.
func TestTarball_Extract_RefusesNilTarball(t *testing.T) {
	var tb *releaseinstall.Tarball
	if err := tb.Extract(t.TempDir()); err == nil {
		t.Fatalf("nil tarball Extract: want error, got nil")
	}
}

// TestTarball_ReadManifest_AfterExtract asserts that after
// Extract + the on-disk WriteManifest round-trip, ReadManifest
// reads back the manifest written by the canary and overwrites
// t.Manifest with the disk truth. PR-B commit 1 surface — the
// doctor probe uses it to verify tarball-side and disk-side
// state agree.
func TestTarball_ReadManifest_AfterExtract(t *testing.T) {
	bin, root, gitSHA, manifestHash := fakeBinDir(t)
	tb, err := releaseinstall.BuildTarball(root, gitSHA, manifestHash, time.Now().UTC())
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if err := releaseinstall.Write(root, tb.Manifest); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if err := tb.Extract(root); err != nil {
		t.Fatalf("extract: %v", err)
	}

	// Mutate the on-disk manifest's Signature (simulating a
	// cosign Verify that ran AFTER the tarball was unpacked)
	// and confirm ReadManifest picks it up.
	diskManifest, err := releaseinstall.Read(root, gitSHA)
	if err != nil {
		t.Fatalf("read disk manifest: %v", err)
	}
	diskManifest.Signature = "https://github.com/poyrazK/faas/.github/workflows/release.yml@refs/tags/v9.9.9"
	if err := releaseinstall.Write(root, diskManifest); err != nil {
		t.Fatalf("write disk manifest: %v", err)
	}

	if err := tb.ReadManifest(root); err != nil {
		t.Fatalf("readmanifest: %v", err)
	}
	if tb.Manifest.Signature != "https://github.com/poyrazK/faas/.github/workflows/release.yml@refs/tags/v9.9.9" {
		t.Errorf("ReadManifest did not pick up disk Signature: got %q", tb.Manifest.Signature)
	}
	// Sanity: Extract wrote the catalog binaries; they are still
	// on disk and untouched by ReadManifest.
	for _, name := range catalogDaemons() {
		if _, err := os.Stat(filepath.Join(bin, name)); err != nil {
			t.Errorf("daemon %s missing after Extract+ReadManifest: %v", name, err)
		}
	}
}

// TestTarball_ReadManifest_RejectsNilTarball is the symmetry
// guard with TestTarball_Extract_RefusesNilTarball.
func TestTarball_ReadManifest_RejectsNilTarball(t *testing.T) {
	var tb *releaseinstall.Tarball
	if err := tb.ReadManifest(t.TempDir()); err == nil {
		t.Fatalf("nil tarball ReadManifest: want error, got nil")
	}
}

// TestTarball_Extract_RejectsZipSlip is the CodeQL CWE-22 test:
// a tarball entry whose Name contains ".." must NEVER escape the
// bin/ directory. CodeQL flagged the original Extract loop at
// tarball.go:284 with "arbitrary file access during archive
// extraction" — the fix is safeArchiveEntryName's regex guards
// (the canonical CodeQL-recognised taint barrier).
func TestTarball_Extract_RejectsZipSlip(t *testing.T) {
	_, root, gitSHA, manifestHash := fakeBinDir(t)
	tb, err := releaseinstall.BuildTarball(root, gitSHA, manifestHash, time.Now().UTC())
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	// Fresh, hand-crafted tarball with one Zip Slip entry
	// appended after the catalog entries. BuildTarball only emits
	// catalog-name entries; we hand-craft to inject the payload.
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, name := range catalogDaemons() {
		body := deterministicBody(name)
		if err := tw.WriteHeader(&tar.Header{
			Name: name, Mode: 0o755, Size: int64(len(body)),
			ModTime:  time.Unix(0, 0).UTC(),
			Typeflag: tar.TypeReg, Format: tar.FormatUSTAR,
		}); err != nil {
			t.Fatalf("write header %s: %v", name, err)
		}
		if _, err := tw.Write(body); err != nil {
			t.Fatalf("write body %s: %v", name, err)
		}
	}
	// Malicious entry: filepath.Base would yield "passwd", but
	// the helper's `strings.Contains(name, "..")` guard rejects
	// the entire entry before any path is computed.
	evil := []byte("zip-slip-payload")
	if err := tw.WriteHeader(&tar.Header{
		Name: "../escape/passwd", Mode: 0o755, Size: int64(len(evil)),
		ModTime:  time.Unix(0, 0).UTC(),
		Typeflag: tar.TypeReg, Format: tar.FormatUSTAR,
	}); err != nil {
		t.Fatalf("write evil header: %v", err)
	}
	if _, err := tw.Write(evil); err != nil {
		t.Fatalf("write evil body: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gz close: %v", err)
	}

	// Reuse the catalog Manifest from BuildTarball but swap Packed
	// for the malicious bytes.
	tb.Packed = buf.Bytes()
	if err := tb.Extract(root); err == nil {
		t.Fatalf("extract with Zip Slip entry: want error, got nil")
	} else if !errors.Is(err, releaseinstall.ErrTarballTampered) {
		t.Fatalf("extract with Zip Slip entry: got %v, want ErrTarballTampered", err)
	}

	// Confirm the escape target was NOT created on disk.
	escapePath := filepath.Join(root, "escape", "passwd")
	if _, err := os.Stat(escapePath); err == nil {
		t.Fatalf("Zip Slip: escape path %s was created", escapePath)
	}
}

// TestSafeArchiveEntryName is the whitebox test for the
// SafeArchiveEntryName helper. CodeQL's go/zipslip data flow
// terminates at the regex guards inside this helper; the
// positive and negative cases below pin down every rule the
// helper enforces.
func TestSafeArchiveEntryName(t *testing.T) {
	t.Helper()
	cases := []struct {
		name    string
		in      string
		wantErr bool
		wantOut string
	}{
		{name: "plain daemon name", in: "apid", wantOut: "apid"},
		{name: "catalog name with subdir", in: "bin/apid", wantOut: "apid"},
		{name: "empty", in: "", wantErr: true},
		{name: "absolute unix", in: "/etc/passwd", wantErr: true},
		{name: "absolute windows", in: `\windows\system32`, wantErr: true},
		{name: "drive letter", in: "C:/windows/notepad", wantErr: true},
		{name: "parent traversal at front", in: "../escape", wantErr: true},
		{name: "parent traversal mid", in: "bin/../../etc/passwd", wantErr: true},
		{name: "parent traversal back", in: "foo/..", wantErr: true},
		{name: "current dir", in: ".", wantErr: true},
		{name: "current dir as subdir", in: "./apid", wantErr: false, wantOut: "apid"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := releaseinstall.SafeArchiveEntryName(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("SafeArchiveEntryName(%q) = %q, nil; want error", tc.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("SafeArchiveEntryName(%q) error: %v", tc.in, err)
			}
			if got != tc.wantOut {
				t.Fatalf("SafeArchiveEntryName(%q) = %q, want %q", tc.in, got, tc.wantOut)
			}
		})
	}
}

// TestTarball_Extract_RejectsAbsolutePath is the companion test
// for absolute-path header Names (e.g. "/etc/passwd"). Defense
// against an attacker who controls a tar header to drop a
// binary anywhere on disk.
func TestTarball_Extract_RejectsAbsolutePath(t *testing.T) {
	_, root, gitSHA, manifestHash := fakeBinDir(t)
	tb, err := releaseinstall.BuildTarball(root, gitSHA, manifestHash, time.Now().UTC())
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, name := range catalogDaemons() {
		body := deterministicBody(name)
		if err := tw.WriteHeader(&tar.Header{
			Name: name, Mode: 0o755, Size: int64(len(body)),
			ModTime:  time.Unix(0, 0).UTC(),
			Typeflag: tar.TypeReg, Format: tar.FormatUSTAR,
		}); err != nil {
			t.Fatalf("write header %s: %v", name, err)
		}
		if _, err := tw.Write(body); err != nil {
			t.Fatalf("write body %s: %v", name, err)
		}
	}
	evil := []byte("absolute-path-payload")
	if err := tw.WriteHeader(&tar.Header{
		Name: "/etc/passwd", Mode: 0o755, Size: int64(len(evil)),
		ModTime:  time.Unix(0, 0).UTC(),
		Typeflag: tar.TypeReg, Format: tar.FormatUSTAR,
	}); err != nil {
		t.Fatalf("write evil header: %v", err)
	}
	if _, err := tw.Write(evil); err != nil {
		t.Fatalf("write evil body: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gz close: %v", err)
	}

	tb.Packed = buf.Bytes()
	if err := tb.Extract(root); err == nil {
		t.Fatalf("extract with absolute path entry: want error, got nil")
	} else if !errors.Is(err, releaseinstall.ErrTarballTampered) {
		t.Fatalf("extract with absolute path entry: got %v, want ErrTarballTampered", err)
	}
}

// TestTarball_Signature_Getter asserts the new PR-B getter:
// Tarball.Signature() returns t.Manifest.Signature verbatim.
// Empty string before Verify runs; non-empty after a successful
// cosign verify-blob stamps the cert identity.
//
// The getter is nil-safe so callers don't have to guard.
func TestTarball_Signature_Getter(t *testing.T) {
	// nil receiver → empty string, no panic.
	var nilTb *releaseinstall.Tarball
	if got := nilTb.Signature(); got != "" {
		t.Errorf("nil Tarball.Signature() = %q, want \"\"", got)
	}
	// Fresh Tarball has empty Signature.
	tb := &releaseinstall.Tarball{}
	if got := tb.Signature(); got != "" {
		t.Errorf("fresh Tarball.Signature() = %q, want \"\"", got)
	}
	// After Verify stamps the field, the getter returns the
	// stamped value. Reuse the existing signedTarball helper so
	// the manifest shape matches the BlessedManifests test.
	tb2, identity := signedTarball(t)
	verifier := &releaseinstall.FixtureCosignVerifier{Identity: identity}
	identity, err := tb2.Verify(context.Background(), verifier)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if got := tb2.Signature(); got != identity {
		t.Errorf("Tarball.Signature() = %q, want %q (Verify's return)", got, identity)
	}
}

// TestTarball_Verify_AllowsStampedSignature proves that the post-verify audit
// identity is not part of the pre-signature bytes. Doctor reads the stamped
// on-disk manifest on later runs and must still validate the original bundle.
func TestTarball_Verify_AllowsStampedSignature(t *testing.T) {
	tb, identity := signedTarball(t)
	verifier := &releaseinstall.FixtureCosignVerifier{Identity: identity}
	if _, err := tb.Verify(context.Background(), verifier); err != nil {
		t.Fatalf("first verify: %v", err)
	}
	if _, err := tb.Verify(context.Background(), verifier); err != nil {
		t.Fatalf("verify after signature stamp: %v", err)
	}
}
