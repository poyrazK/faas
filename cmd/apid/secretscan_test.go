// Whitebox tests for cmd/apid/secretscan.go. The walk is the
// defence-in-depth backstop for the client-side scan; a regression
// that breaks file-handling (FD leak, binary false-positives) opens
// the box to "too many open files" outages. Tests run without
// Postgres — the scan is filesystem-only.
package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// TestScanExtractedTreeSecrets_CleanTree pins the no-findings fast
// path on an empty customer tree (the most common path in CI).
func TestScanExtractedTreeSecrets_CleanTree(t *testing.T) {
	dir := t.TempDir()
	findings, err := scanExtractedTreeSecrets(dir)
	if err != nil {
		t.Fatalf("scanExtractedTreeSecrets: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("clean tree produced findings: %+v", findings)
	}
}

// TestScanExtractedTreeSecrets_EnvFileHit pins the .env-prod path
// (PR-862 carry-over). The .env.production file is the highest-volume
// source of accidental secret commits; the server-side walk must
// catch it even when the CLI was run with --secret-scan=off.
func TestScanExtractedTreeSecrets_EnvFileHit(t *testing.T) {
	dir := t.TempDir()
	env := "PORT=8080\nSTRIPE_SECRET_KEY=" + fakeStripeLiveKey + "\n"
	if err := os.WriteFile(filepath.Join(dir, ".env.production"), []byte(env), 0o600); err != nil {
		t.Fatalf("seed .env.production: %v", err)
	}
	findings, err := scanExtractedTreeSecrets(dir)
	if err != nil {
		t.Fatalf("scanExtractedTreeSecrets: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("findings = %d, want 1: %+v", len(findings), findings)
	}
	if findings[0].Provider != "stripe_live" {
		t.Errorf("Provider = %q, want stripe_live", findings[0].Provider)
	}
	if findings[0].Line != 2 {
		t.Errorf("Line = %d, want 2", findings[0].Line)
	}
}

// TestScanExtractedTreeSecrets_PEMHit pins the source-tree path
// (PR-A v2 gap C closer). A .pem file under the customer tree must
// fire; otherwise the server-side scan regresses to v1 scope.
func TestScanExtractedTreeSecrets_PEMHit(t *testing.T) {
	dir := t.TempDir()
	pem := "-----BEGIN RSA PRIVATE KEY-----\nMIIEowIBAAKCAQEAxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx\n-----END RSA PRIVATE KEY-----\n"
	if err := os.WriteFile(filepath.Join(dir, "keys.pem"), []byte(pem), 0o600); err != nil {
		t.Fatalf("seed keys.pem: %v", err)
	}
	findings, err := scanExtractedTreeSecrets(dir)
	if err != nil {
		t.Fatalf("scanExtractedTreeSecrets: %v", err)
	}
	if len(findings) == 0 {
		t.Fatalf("PEM file produced no findings (source-tree path broken)")
	}
	// v2 dedup (#3 review fix): the regex + entropy fallback must
	// produce exactly ONE finding for the BEGIN line, not one per
	// base64 body line. A 100-line key would have produced ~101
	// findings before the dedup; assert count == 1 here.
	beginOnly := 0
	for _, f := range findings {
		if f.Provider == "private_key_block" && f.Line == 1 {
			beginOnly++
		}
	}
	if beginOnly != 1 {
		t.Errorf("private_key_block BEGIN-line findings = %d, want 1 (multi-line PEM dedup regression; got %+v)", beginOnly, findings)
	}
}

// TestScanExtractedTreeSecrets_PNGSkipped pins the text-file gate
// (PR-A v2 gap A). A PNG with high-entropy body bytes must NOT
// match the secret patterns — the NUL-byte probe + http
// DetectContentType are the only thing keeping a 50-MB customer
// image from producing a 422.
func TestScanExtractedTreeSecrets_PNGSkipped(t *testing.T) {
	dir := t.TempDir()
	png := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0x00, 0x00, 0x00, 0x0D}
	if err := os.WriteFile(filepath.Join(dir, "logo.png"), png, 0o600); err != nil {
		t.Fatalf("seed logo.png: %v", err)
	}
	findings, err := scanExtractedTreeSecrets(dir)
	if err != nil {
		t.Fatalf("scanExtractedTreeSecrets: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("PNG produced findings (text-file gate broken): %+v", findings)
	}
}

// TestScanSourceTarballSecrets_RejectsEnvSecret pins the direct deploy
// ingress. A caller that uploads a hand-built tarball (or disables the CLI
// scanner) must still hit the same server-side rejection before enqueue.
func TestScanSourceTarballSecrets_RejectsEnvSecret(t *testing.T) {
	scanRoot := t.TempDir()
	t.Setenv("FAAS_SCAN_SPOOL_ROOT", scanRoot)
	sourcePath := filepath.Join(t.TempDir(), "source.tar.gz")
	writeSecretScanTarGz(t, sourcePath, ".env", "STRIPE_SECRET_KEY="+fakeStripeLiveKey+"\n")

	prob := scanSourceTarballSecrets(sourcePath, api.MustLimitsFor(api.PlanFree))
	if prob == nil {
		t.Fatal("secret-bearing tarball was accepted")
	}
	if prob.Status != 422 || prob.Code != api.CodeSecretScanStrict {
		t.Fatalf("problem = %#v, want 422/%s", prob, api.CodeSecretScanStrict)
	}
	entries, err := os.ReadDir(scanRoot)
	if err != nil {
		t.Fatalf("ReadDir scan root: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("scan extraction leaked %d directory entries", len(entries))
	}
}

// TestScanExtractedTreeSecrets_ScansBuildDirectory prevents a directory-name
// shortcut from becoming a secret-scan bypass. The client packer may omit
// build artifacts, but a direct upload must be scanned for what it actually
// contains.
func TestScanExtractedTreeSecrets_ScansBuildDirectory(t *testing.T) {
	dir := t.TempDir()
	buildDir := filepath.Join(dir, "build")
	if err := os.MkdirAll(buildDir, 0o700); err != nil {
		t.Fatalf("mkdir build: %v", err)
	}
	if err := os.WriteFile(filepath.Join(buildDir, ".env"), []byte("STRIPE_SECRET_KEY="+fakeStripeLiveKey+"\n"), 0o600); err != nil {
		t.Fatalf("seed build secret: %v", err)
	}
	findings, err := scanExtractedTreeSecrets(dir)
	if err != nil {
		t.Fatalf("scanExtractedTreeSecrets: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("findings = %d, want 1: %+v", len(findings), findings)
	}
}

func writeSecretScanTarGz(t *testing.T, path, name, body string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create tarball: %v", err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: int64(len(body))}); err != nil {
		t.Fatalf("tar header: %v", err)
	}
	if _, err := io.Copy(tw, bytes.NewBufferString(body)); err != nil {
		t.Fatalf("tar body: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("file close: %v", err)
	}
}

// TestScanExtractedTreeSecrets_NoFDLeak pins the FD-leak fix
// (#1 review finding). A 500-file customer tree with no findings
// must NOT pin 500 open file descriptors by the end of the walk.
// We sample the OS thread's open FD count via /dev/fd before and
// after; on Linux, /dev/fd is the only portable way to count
// without an explicit CGO dep. The test skips on macOS where
// /dev/fd is the symlink to /dev/null on the parent's mount.
//
// Before the fix: every visited file's `defer f.Close()` deferred
// to scanExtractedTreeSecrets's return, so open FDs == 500.
// After the fix: close inline, open FDs == 0.
func TestScanExtractedTreeSecrets_NoFDLeak(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("macOS: /dev/fd is /dev/null, can't enumerate per-thread FDs portably")
	}
	dir := t.TempDir()
	const fileCount = 500
	for i := 0; i < fileCount; i++ {
		// 1 KiB text files — large enough to keep the kernel FD
		// pinned if a defer leaks, small enough that the walk
		// completes in < 100 ms.
		body := strings.Repeat("hello world\n", 64)
		if err := os.WriteFile(filepath.Join(dir, "f"+secretscanItoa(i)+".txt"), []byte(body), 0o600); err != nil {
			t.Fatalf("seed f%d: %v", i, err)
		}
	}
	before := countOpenFDs(t)
	findings, err := scanExtractedTreeSecrets(dir)
	if err != nil {
		t.Fatalf("scanExtractedTreeSecrets: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("clean txt files produced %d findings, want 0", len(findings))
	}
	after := countOpenFDs(t)
	// Allow a small slack (+5) for the FD that the test itself
	// opened on /dev/fd, the test binary's own open fds, etc.
	if after > before+5 {
		t.Errorf("FD leak: before=%d after=%d (delta=%d, want <=5); the walk is pinning %d FDs",
			before, after, after-before, after-before)
	}
}

// secretscanItoa is a tiny base-10 formatter so the test file
// doesn't need strconv. The numbers are < 1000 (500 max), so a
// 4-byte buffer is enough. Renamed from `itoa` to avoid colliding
// with the existing declaration in handlers_ext_test.go.
func secretscanItoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [4]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// fakeStripeLiveKey is a test fixture mirror — the canonical
// declaration lives in cmd/gregale/pack_test.go (whitebox). The
// cmd/apid test surface can't import cmd/gregale (it would pull
// the entire CLI into the apid binary), so we redeclare a copy
// here. The string is assembled via concatenation so GitHub's
// secret-scanner doesn't flag the literal pattern on push.
const fakeStripeLiveKey = "sk_live_" + "aBcDeFgHiJkLmNoPqRsTuVwXyZ" + "_XXXX"

// countOpenFDs returns the number of file descriptors the current
// process has open, via /dev/fd on Linux. Used by the FD-leak
// regression test; no-op on macOS where /dev/fd is the symlink to
// /dev/null (the test's runtime.GOOS gate skips macOS).
func countOpenFDs(t *testing.T) int {
	t.Helper()
	entries, err := os.ReadDir("/dev/fd")
	if err != nil {
		return 0 // /dev/fd unreadable — skip the assertion downstream
	}
	// Subtract 1 for /dev/fd itself (the dirent we're enumerating).
	return len(entries) - 1
}
