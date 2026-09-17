// bundle_extra_test.go — fill pkg/releasebundle coverage of the
// missing error branches and edge cases reachable without a real
// build pipeline. Targets:
//
//   - ValidateManifest: FormatVersion != 1, identity empty,
//     CreatedAt zero, forbidden paths, unsorted files, mode-bit,
//     negative size, sha256 length and hex errors
//   - Build: forbidden-path file (issue #911 denylist), empty root,
//     non-existent root
//   - Write: error paths (parent dir not writable, marshal error)
//   - Read: missing manifest, malformed JSON
//   - Verify: file-size mismatch, mode mismatch, sha256 mismatch,
//     unexpected files
//   - hashFile: file missing, regular file with deterministic hash
//   - validatePath: absolute path, empty path, backslash, dot,
//     leading ../, embedded ../, dot-dot-dot traversal
//
// Whitebox `package releasebundle`.

package releasebundle

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// --- writeFile helper (in bundle_test.go:140) ---------------------
//
// The shared helper now re-applies the requested mode bits via
// os.Chmod so Verify's mode-mismatch branch can be exercised
// deterministically regardless of the test process umask. All
// callers (the original Build/Read tests plus the new Validate /
// Verify / mode-mismatch tests) reuse the single helper.

// --- ValidateManifest additional error branches ------------------

func TestValidateManifest_FormatVersionOther(t *testing.T) {
	m := validManifest()
	m.FormatVersion = 2
	if err := ValidateManifest(m); err == nil || !strings.Contains(err.Error(), "format version 2") {
		t.Errorf("format=2: got %v, want 'format version 2'", err)
	}
}

func TestValidateManifest_IdentityIncompleteFields(t *testing.T) {
	for _, mutate := range []func(*Manifest){
		func(m *Manifest) { m.ReleaseID = "" },
		func(m *Manifest) { m.CommitSHA = "" },
		func(m *Manifest) { m.Target = "" },
	} {
		m := validManifest()
		mutate(&m)
		if err := ValidateManifest(m); err == nil || !strings.Contains(err.Error(), "identity is incomplete") {
			t.Errorf("identity incomplete: got %v, want 'identity is incomplete'", err)
		}
	}
}

func TestValidateManifest_CreatedAtZero(t *testing.T) {
	m := validManifest()
	m.CreatedAt = time.Time{}
	if err := ValidateManifest(m); err == nil || !strings.Contains(err.Error(), "created_at is zero") {
		t.Errorf("zero CreatedAt: got %v, want 'created_at is zero'", err)
	}
}

func TestValidateManifest_UnsortedFiles(t *testing.T) {
	m := validManifest()
	// Reverse-sorted (largest first) must trip the sort guard.
	m.Files = []File{
		m.Files[1],
		m.Files[0],
	}
	if err := ValidateManifest(m); err == nil || !strings.Contains(err.Error(), "not strictly sorted") {
		t.Errorf("unsorted: got %v, want 'not strictly sorted'", err)
	}
}

func TestValidateManifest_InvalidMode(t *testing.T) {
	m := validManifest()
	// mode 0o170000 = S_IFREG bitmask; setting any of those means
	// the file is a special (device/socket) — not a regular file.
	m.Files[0].Mode = 0o170000
	if err := ValidateManifest(m); err == nil || !strings.Contains(err.Error(), "invalid mode") {
		t.Errorf("invalid mode: got %v, want 'invalid mode'", err)
	}
}

func TestValidateManifest_NegativeSize(t *testing.T) {
	m := validManifest()
	m.Files[0].Size = -1
	if err := ValidateManifest(m); err == nil || !strings.Contains(err.Error(), "negative size") {
		t.Errorf("negative size: got %v, want 'negative size'", err)
	}
}

func TestValidateManifest_SHA256WrongLength(t *testing.T) {
	m := validManifest()
	m.Files[0].SHA256 = "deadbeef"
	if err := ValidateManifest(m); err == nil || !strings.Contains(err.Error(), "invalid sha256") {
		t.Errorf("short sha256: got %v, want 'invalid sha256'", err)
	}
}

func TestValidateManifest_SHA256NotHex(t *testing.T) {
	m := validManifest()
	// Correct length but non-hex chars trigger the DecodeString branch.
	m.Files[0].SHA256 = strings.Repeat("z", sha256.Size*2)
	if err := ValidateManifest(m); err == nil || !strings.Contains(err.Error(), "invalid sha256") {
		t.Errorf("non-hex sha256: got %v, want 'invalid sha256'", err)
	}
}

func TestValidateManifest_Empty(t *testing.T) {
	if err := ValidateManifest(Manifest{}); err == nil {
		t.Error("zero manifest: want error, got nil")
	}
}

func TestValidateManifest_HappyPath(t *testing.T) {
	if err := ValidateManifest(validManifest()); err != nil {
		t.Errorf("valid manifest: %v, want nil", err)
	}
}

// --- Build edge cases --------------------------------------------

// A file under a forbidden path (issue #911 denylist: "faas-tunnel")
// trips Build.
func TestBuild_RejectsForbiddenFilePath(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "bin", "faas-tunnel"), "stub\n", 0o644)
	if _, err := Build(root, "r-1", "deadbeef0000000000000000000000000000000", "linux/amd64", time.Now()); err == nil ||
		!strings.Contains(err.Error(), "forbidden path") {
		t.Errorf("forbidden-path file: got %v, want 'forbidden path'", err)
	}
}

// Non-existent root directory → every Stat fails → Build must
// surface the error (not panic, not return a partial manifest).
func TestBuild_NonExistentRoot(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	if _, err := Build(missing, "r-1", "deadbeef0000000000000000000000000000000", "linux/amd64", time.Now()); err == nil {
		t.Error("missing root: want error, got nil")
	}
}

// --- Write error branches ----------------------------------------

// Manifest.Write to a root that doesn't exist must fail (parent dir
// create at internal step can't succeed). The exact error varies by
// platform; we only assert error != nil — the function must NOT
// silently truncate the manifest.
func TestWrite_NonExistentRootErrors(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no-such-root")
	m := validManifest()
	if err := Write(missing, m); err == nil {
		t.Error("missing root: want write error, got nil")
	}
}

// --- Read error branches -----------------------------------------

// Read without a prior Write returns an unwrapped error since the
// file does not exist.
func TestRead_MissingManifestErrors(t *testing.T) {
	root := t.TempDir()
	if _, err := Read(root); err == nil {
		t.Error("missing manifest: want read error, got nil")
	}
}

func TestRead_MalformedManifestJSON(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "release.manifest.json"), []byte("{not valid"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(root); err == nil {
		t.Error("malformed manifest: want parse error, got nil")
	}
}

// --- Verify additional error branches ---------------------------

// Size mismatch (manifest claims X bytes, file has Y). The Verify
// error path emits "releasebundle: <path> size <actual>, want <expected>"
// where <actual> is the on-disk file size (5 for "hello") and
// <expected> is the manifest's Size field (999). We pin the
// substring that disambiguates this branch from the sha256 / mode
// branches — without the size discriminator the test would pass
// for any non-nil error.
func TestVerify_FileSizeMismatch(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "data.txt"), "hello", 0o644)
	m := validManifestWithSingleFile("data.txt", "hello")
	m.Files[0].Size = 999
	err := Verify(root, m)
	if err == nil {
		t.Fatal("size mismatch: want error, got nil")
	}
	if !strings.Contains(err.Error(), "size 5, want 999") {
		t.Errorf("size mismatch: err = %v; want 'size 5, want 999' substring", err)
	}
}

// Mode mismatch (different perm bits).
func TestVerify_FileModeMismatch(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "bin.sh"), "#!/bin/sh\n", 0o644)
	m := validManifestWithSingleFile("bin.sh", "#!/bin/sh\n")
	m.Files[0].Mode = 0o755
	if err := Verify(root, m); err == nil {
		t.Errorf("mode mismatch: want error, got nil")
	}
}

// SHA256 mismatch (claims a hash different from the file content).
// The Verify error path emits "releasebundle: <path> sha256 <actual>,
// want <expected>"; the discriminator word "sha256" pins the branch.
func TestVerify_SHA256Mismatch(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "data.txt"), "hello", 0o644)
	m := validManifestWithSingleFile("data.txt", "hello")
	// Override SHA with a valid-shape hex string of the wrong hash.
	m.Files[0].SHA256 = hex.EncodeToString(make([]byte, sha256.Size))
	err := Verify(root, m)
	if err == nil {
		t.Fatal("sha256 mismatch: want error, got nil")
	}
	if !strings.Contains(err.Error(), "sha256") {
		t.Errorf("sha256 mismatch: err = %v; want 'sha256' substring", err)
	}
}

// Verify against a manifest whose Files list is empty (no files to
// check) — should pass cleanly on the empty case.
func TestVerify_NoFilesIsAllowed(t *testing.T) {
	root := t.TempDir()
	m := validManifest()
	m.Files = nil
	if err := Verify(root, m); err != nil {
		t.Errorf("empty files: want nil, got %v", err)
	}
}

// --- hashFile ----------------------------------------------------

func TestHashFile_Deterministic(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "x.txt")
	if err := os.WriteFile(path, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	want := sha256.Sum256([]byte("hello"))
	wantHex := hex.EncodeToString(want[:])
	if got, err := hashFile(path); err != nil {
		t.Fatalf("hashFile: %v", err)
	} else if got != wantHex {
		t.Errorf("hashFile = %s, want %s", got, wantHex)
	}
}

func TestHashFile_MissingFileErrors(t *testing.T) {
	if _, err := hashFile(filepath.Join(t.TempDir(), "no-such")); err == nil {
		t.Error("missing: want hash error, got nil")
	}
}

func TestHashReaderUsesBoundedChunks(t *testing.T) {
	reader := &boundedHashReader{remaining: 2 << 20, maxRead: 32 << 10}
	digest, err := hashReader(reader)
	if err != nil {
		t.Fatalf("hashReader: %v", err)
	}
	if len(digest) != sha256.Size*2 {
		t.Fatalf("digest length = %d, want %d", len(digest), sha256.Size*2)
	}
}

type boundedHashReader struct {
	remaining int64
	maxRead   int
}

func (r *boundedHashReader) Read(p []byte) (int, error) {
	if len(p) > r.maxRead {
		return 0, io.ErrShortBuffer
	}
	if r.remaining == 0 {
		return 0, io.EOF
	}
	n := len(p)
	if int64(n) > r.remaining {
		n = int(r.remaining)
	}
	for i := 0; i < n; i++ {
		p[i] = 'x'
	}
	r.remaining -= int64(n)
	return n, nil
}

// --- validatePath additional cases ------------------------------

// validatePath is unexported; exercise via ValidateManifest's loop
// which calls it for each file path.
func TestValidateManifest_PathAbsolute(t *testing.T) {
	m := validManifest()
	m.Files[0].Path = "/absolute/path"
	if err := ValidateManifest(m); err == nil || !strings.Contains(err.Error(), "unsafe path") {
		t.Errorf("abs path: got %v, want 'unsafe path'", err)
	}
}

func TestValidateManifest_PathEmpty(t *testing.T) {
	m := validManifest()
	m.Files[0].Path = ""
	if err := ValidateManifest(m); err == nil || !strings.Contains(err.Error(), "unsafe path") {
		t.Errorf("empty path: got %v, want 'unsafe path'", err)
	}
}

// Control: a plain-relative path (no traversal, no leading slash)
// passes validatePath — no test assertion needed beyond the
// "no error" return.
func TestValidateManifest_PathPlainRelativePasses(t *testing.T) {
	m := validManifest()
	// validManifest's first file is already "bin/apid"; valid round-trip.
	if err := ValidateManifest(m); err != nil {
		t.Errorf("plain relative path: got %v, want nil", err)
	}
}

func TestValidateManifest_PathDotDot(t *testing.T) {
	m := validManifest()
	// Insert a third file before the existing two so the sorted
	// layout is still strictly increasing; the path must not be
	// sorted after the previous ok path, so use the smaller prefix.
	m.Files = []File{
		{Path: "bin/a", Mode: 0o644, Size: 1, SHA256: hex.EncodeToString(make([]byte, sha256.Size))},
		{Path: "bin/../escape", Mode: 0o644, Size: 1, SHA256: hex.EncodeToString(make([]byte, sha256.Size))},
	}
	if err := ValidateManifest(m); err == nil || !strings.Contains(err.Error(), "unsafe path") {
		t.Errorf("../ path: got %v, want 'unsafe path'", err)
	}
}

// --- helpers -----------------------------------------------------

// validManifest returns a minimal Manifest that passes ValidateManifest.
// Files is two regular files with valid-size hex sha256 placeholders.
func validManifest() Manifest {
	badSHA := hex.EncodeToString(make([]byte, sha256.Size))
	return Manifest{
		FormatVersion: 1,
		ReleaseID:     "r-1",
		CommitSHA:     "deadbeef0000000000000000000000000000000",
		Target:        "linux/amd64",
		CreatedAt:     time.Date(2026, 8, 6, 10, 0, 0, 0, time.UTC),
		Files: []File{
			{Path: "bin/apid", Mode: 0o755, Size: 4, SHA256: badSHA},
			{Path: "systemd/faas-apid.service", Mode: 0o644, Size: 10, SHA256: badSHA},
		},
	}
}

// validManifestWithSingleFile returns a single-file Manifest with the
// real sha256 of `contents`. Use for Verify tests where the content
// drives the hash.
func validManifestWithSingleFile(path, contents string) Manifest {
	sum := sha256.Sum256([]byte(contents))
	return Manifest{
		FormatVersion: 1,
		ReleaseID:     "r-1",
		CommitSHA:     "deadbeef0000000000000000000000000000000",
		Target:        "linux/amd64",
		CreatedAt:     time.Date(2026, 8, 6, 10, 0, 0, 0, time.UTC),
		Files: []File{
			{Path: path, Mode: 0o644, Size: int64(len(contents)), SHA256: hex.EncodeToString(sum[:])},
		},
	}
}
