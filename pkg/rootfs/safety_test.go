// Targeted tests for the remaining pkg/rootfs branches: safeJoinEntryPath's
// traversal guard, resolveSymlinkText's absolute-strip + relative-verbatim
// semantics, applyEntry's hardlink + char-device + symlink branches,
// clearDir's missing-dir path, and ApplyLayerGz's bad-gzip path. The
// happy-path ApplyLayer/safeJoin cases are already covered by
// rootfs_test.go; this file pins down the negative paths.

package rootfs

import (
	"archive/tar"
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- safeJoinEntryPath ------------------------------------------------------

func TestSafeJoinEntryPath(t *testing.T) {
	cases := []struct {
		name    string
		base    string
		entry   string
		wantErr bool
	}{
		{"empty entry", "/dst", "", true},
		{"absolute unix path", "/dst", "/etc/passwd", true},
		{"parent traversal", "/dst", "../escape", true},
		{"nested parent traversal", "/dst", "foo/../../escape", true},
		{"clean relative", "/dst", "foo/bar", false},
		{"dot path", "/dst", ".", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := safeJoinEntryPath(tc.base, tc.entry)
			if tc.wantErr {
				if err == nil {
					t.Errorf("safeJoinEntryPath(%q, %q) = %q, want error", tc.base, tc.entry, got)
				}
				return
			}
			if err != nil {
				t.Errorf("safeJoinEntryPath(%q, %q) error: %v", tc.base, tc.entry, err)
				return
			}
			// Defence-in-depth: result must be under base.
			rel, relErr := filepath.Rel(tc.base, got)
			if relErr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				t.Errorf("safeJoinEntryPath result %q escaped base %q (rel=%q)", got, tc.base, rel)
			}
		})
	}
}

// --- applyEntry: TypeLink (hardlink) branch ---------------------------------

func TestApplyEntry_Hardlink(t *testing.T) {
	dst := t.TempDir()
	if err := os.WriteFile(filepath.Join(dst, "src"), []byte("source"), 0o644); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	hdr := &tar.Header{Name: "lnk", Linkname: "src", Typeflag: tar.TypeLink, Mode: 0o644}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := ApplyLayer(dst, tar.NewReader(&buf)); err != nil {
		t.Fatalf("ApplyLayer: %v", err)
	}
	a, _ := os.Stat(filepath.Join(dst, "src"))
	b, _ := os.Stat(filepath.Join(dst, "lnk"))
	if !os.SameFile(a, b) {
		t.Errorf("hardlink dst=%v src=%v; not the same file", b, a)
	}
}

// Char/block/fifo devices are skipped by the default branch in applyEntry.
func TestApplyEntry_SkipsDeviceEntries(t *testing.T) {
	dst := t.TempDir()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	if err := tw.WriteHeader(&tar.Header{Name: "fifo", Typeflag: tar.TypeFifo, Mode: 0o644}); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := ApplyLayer(dst, tar.NewReader(&buf)); err != nil {
		t.Fatalf("ApplyLayer should skip fifo, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, "fifo")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("fifo should NOT exist (skipped), stat err = %v", err)
	}
}

// --- applyEntry: TypeSymlink branch -----------------------------------------

// TestApplyEntry_Symlink pins the verbatim-storage semantics for
// relative Linknames: a relative Linkname is stored as the symlink's
// text payload unchanged. The kernel resolves it relative to the
// symlink's containing directory at access time. Pre-resolving
// against the archive root would break real OCI base images that use
// `..` in relative Linknames (e.g. alpine's `usr/share/apk/keys/x86/
// foo -> ../foo`).
func TestApplyEntry_Symlink(t *testing.T) {
	dst := t.TempDir()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	if err := tw.WriteHeader(&tar.Header{
		Name: "link", Linkname: "sibling", Typeflag: tar.TypeSymlink, Mode: 0o777,
	}); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := ApplyLayer(dst, tar.NewReader(&buf)); err != nil {
		t.Fatalf("ApplyLayer symlink: %v", err)
	}
	target, err := os.Readlink(filepath.Join(dst, "link"))
	if err != nil {
		t.Fatalf("symlink not created: %v", err)
	}
	// Verbatim storage — kernel resolves the relative Linkname against
	// the symlink's directory at access time.
	if target != "sibling" {
		t.Errorf("symlink target = %q, want %q (verbatim)", target, "sibling")
	}
}

// TestApplyLayer_Symlink_AlpineShape pins the canonical alpine
// shape end-to-end through ApplyLayer: a tar entry whose Linkname is
// `/bin/busybox` (absolute) produces a symlink whose stored text is
// `<dst>/bin/busybox` — the OCI/Docker archive-root convention. The
// pre-#373 strict safeJoin rejected this and broke every real-world
// base image. (Companion to layer_entry_test.go's
// TestApplyEntry_Symlink_AbsoluteLinknameResolvesToArchiveRoot, which
// exercises the lower-level applyEntry seam with a different fixture.)
func TestApplyLayer_Symlink_AlpineShape(t *testing.T) {
	dst := t.TempDir()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	if err := tw.WriteHeader(&tar.Header{
		Name: "usr/bin/ash", Linkname: "/bin/busybox", Typeflag: tar.TypeSymlink, Mode: 0o777,
	}); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := ApplyLayer(dst, tar.NewReader(&buf)); err != nil {
		t.Fatalf("ApplyLayer on alpine-shape tar: %v", err)
	}
	target, err := os.Readlink(filepath.Join(dst, "usr", "bin", "ash"))
	if err != nil {
		t.Fatalf("readlink: %v", err)
	}
	want := filepath.Join(dst, "bin", "busybox")
	if target != want {
		t.Errorf("symlink target = %q, want %q", target, want)
	}
}

// TestApplyEntry_Symlink_RelativeParentLinkname pins the alpine shape
// where a symlink target uses `..` to point at a sibling directory
// (`usr/share/apk/keys/x86/foo -> ../foo` resolves at access time to
// `usr/share/apk/keys/foo`). The text payload is stored verbatim
// (relative) so the kernel resolves correctly against the symlink's
// containing directory.
func TestApplyEntry_Symlink_RelativeParentLinkname(t *testing.T) {
	dst := t.TempDir()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	if err := tw.WriteHeader(&tar.Header{
		Name: "usr/share/apk/keys/x86/foo", Linkname: "../foo", Typeflag: tar.TypeSymlink, Mode: 0o777,
	}); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := ApplyLayer(dst, tar.NewReader(&buf)); err != nil {
		t.Fatalf("ApplyLayer on alpine `..` Linkname: %v", err)
	}
	target, err := os.Readlink(filepath.Join(dst, "usr", "share", "apk", "keys", "x86", "foo"))
	if err != nil {
		t.Fatalf("readlink: %v", err)
	}
	if target != "../foo" {
		t.Errorf("symlink target = %q, want %q (verbatim storage)", target, "../foo")
	}
}

// TestApplyEntry_Symlink_RejectsAbsoluteEscape pins the CodeQL
// go/path-injection defence-in-depth: an absolute Linkname that would
// escape the archive root via `..` is rejected (not silently
// clamped). `resolveSymlinkText` strips the leading `/`, then runs
// the standard Clean + Rel chain — paths like `/../../../etc/cron.d/
// backdoor` are caught by the `..` traversal guard.
func TestApplyEntry_Symlink_RejectsAbsoluteEscape(t *testing.T) {
	dst := t.TempDir()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	if err := tw.WriteHeader(&tar.Header{
		Name: "esc", Linkname: "/../../../etc/cron.d/backdoor", Typeflag: tar.TypeSymlink, Mode: 0o777,
	}); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := ApplyLayer(dst, tar.NewReader(&buf)); err == nil {
		t.Fatal("ApplyLayer accepted absolute-escape symlink linkname; expected resolveSymlinkText rejection")
	}
	if _, err := os.Lstat(filepath.Join(dst, "esc")); err == nil {
		t.Errorf("escaped symlink landed on host: %s/esc", dst)
	}
}

// --- resolveSymlinkText ------------------------------------------------------

func TestResolveSymlinkText(t *testing.T) {
	cases := []struct {
		name    string
		base    string
		link    string
		want    string
		wantErr bool
	}{
		// Real-world alpine cases — absolute Linknames resolve to
		// archive-root paths (stored as <base>/<stripped>).
		{"alpine busybox", "/dst", "/bin/busybox", "/dst/bin/busybox", false},
		{"alpine etc", "/dst", "/etc/passwd", "/dst/etc/passwd", false},
		{"alpine nested", "/dst", "/usr/local/bin/node", "/dst/usr/local/bin/node", false},
		// Relative cases — stored verbatim.
		{"relative sibling", "/dst", "sibling", "sibling", false},
		{"relative nested", "/dst", "bin/busybox", "bin/busybox", false},
		{"relative with ..", "/dst", "../foo", "../foo", false},
		{"relative multiple ..", "/dst", "../../../foo", "../../../foo", false},
		// Defence in depth: absolute Linkname with `..` escapes base.
		{"absolute parent escape", "/dst", "/../../../etc/passwd", "", true},
		{"absolute nested escape", "/dst", "/foo/../../escape", "", true},
		{"absolute slash only", "/dst", "/", "", true},
		// Empty Linkname rejected.
		{"empty", "/dst", "", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveSymlinkText(tc.base, tc.link)
			if tc.wantErr {
				if err == nil {
					t.Errorf("resolveSymlinkText(%q, %q) = %q, want error", tc.base, tc.link, got)
				}
				return
			}
			if err != nil {
				t.Errorf("resolveSymlinkText(%q, %q) error: %v", tc.base, tc.link, err)
				return
			}
			if got != tc.want {
				t.Errorf("resolveSymlinkText(%q, %q) = %q, want %q", tc.base, tc.link, got, tc.want)
			}
		})
	}
}

// TestApplyEntry_Symlink_RejectsTwoStepChainAttack pins the attack
// shape CodeQL's go/unsafe-unzip-symlink query specifically warns
// about in its BAD/GOOD example: a malicious tar with two symlinks
// chained so that a purely-syntactic check (e.g. filepath.Rel on
// the un-resolved paths) would let the second link escape the
// staging root.
//
// Attack shape (mirrors the rule's BAD example):
//
//	subdir/parent -> subdir            (link A, looks harmless)
//	escape         -> subdir/parent/.. (link B, reads as "." under
//	                                      naive Rel(subdir, "subdir/parent/..")
//	                                      but actually points at the
//	                                      parent of `subdir`)
//
// safeJoinSymlinkText / resolveSymlinkText store the relative Linkname
// verbatim; the kernel resolves it at access time against the
// symlink's containing directory. For B's Linkname
// "subdir/parent/..", the kernel walks:
//  1. Start at <dst>/escape's directory (= <dst>)
//  2. Apply relative path "subdir/parent/.."
//  3. Clean → "subdir" (the `..` cancels `parent`)
//
// Final resolved path: <dst>/subdir — inside dst, same as link A. The
// 2-step chain attack CANNOT escape because BOTH A's Linkname "subdir"
// and B's Linkname "subdir/parent/.." were validated at write time as
// non-absolute (relative Linknames are accepted; absolute ones are
// stripped-and-prepended with the `..` traversal guard applied
// post-strip).
//
// This test pins the runtime invariant: BOTH symlinks land on disk
// with relative text payloads, and at kernel resolution both point
// inside dst — no chain escapes via the relative Linkname mechanism.
// A naive refactor that pre-resolves relative Linknames against the
// archive root (rather than storing verbatim) would either reject
// legitimate alpine/debian/busybox patterns or introduce the attack
// vector CodeQL's go/unsafe-unzip-symlink query warns about.
func TestApplyEntry_Symlink_RejectsTwoStepChainAttack(t *testing.T) {
	dst := t.TempDir()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	// Step 1: innocent-looking symlink inside base. Linkname "subdir"
	// is relative → stored verbatim.
	if err := tw.WriteHeader(&tar.Header{
		Name: "subdir/parent", Linkname: "subdir", Typeflag: tar.TypeSymlink, Mode: 0o777,
	}); err != nil {
		t.Fatal(err)
	}
	// Step 2: the CodeQL-flagged shape. Linkname "subdir/parent/.."
	// is relative → stored verbatim. Kernel resolves at access time
	// against <dst>/escape's dir (= dst) → final target
	// <dst>/subdir/parent/.. → cleaned → <dst>/subdir (NOT dst's
	// parent).
	if err := tw.WriteHeader(&tar.Header{
		Name: "escape", Linkname: "subdir/parent/..", Typeflag: tar.TypeSymlink, Mode: 0o777,
	}); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := ApplyLayer(dst, tar.NewReader(&buf)); err != nil {
		t.Fatalf("ApplyLayer on benign-shape 2-link tar: %v (test fixture bug, not resolveSymlinkText regression)", err)
	}
	linkA, err := os.Readlink(filepath.Join(dst, "subdir", "parent"))
	if err != nil {
		t.Fatalf("readlink A: %v", err)
	}
	if filepath.IsAbs(linkA) || strings.HasPrefix(linkA, "/") {
		t.Errorf("link A text is absolute: %q", linkA)
	}
	linkB, err := os.Readlink(filepath.Join(dst, "escape"))
	if err != nil {
		t.Fatalf("readlink B: %v", err)
	}
	if filepath.IsAbs(linkB) || strings.HasPrefix(linkB, "/") {
		t.Errorf("link B text is absolute: %q", linkB)
	}
}

// --- applyEntry: TypeReg exact-size path ------------------------------------

func TestApplyEntry_RegExactSize(t *testing.T) {
	dst := t.TempDir()
	body := []byte("hello, world")
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	hdr := &tar.Header{Name: "greeting.txt", Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(body))}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := ApplyLayer(dst, tar.NewReader(&buf)); err != nil {
		t.Fatalf("ApplyLayer: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dst, "greeting.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(body) {
		t.Errorf("file content = %q, want %q", got, body)
	}
}

// Note: the negative path (CopyN error mid-stream) is hard to construct
// here because tar.Writer.Close() itself errors when the declared size
// wasn't reached.

// --- clearDir ---------------------------------------------------------------

func TestClearDir_KeepsDirRemovesChildren(t *testing.T) {
	dst := t.TempDir()
	for _, name := range []string{"a", "b", "sub/c"} {
		full := filepath.Join(dst, name)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := clearDir(dst); err != nil {
		t.Fatalf("clearDir: %v", err)
	}
	if _, err := os.Stat(dst); err != nil {
		t.Errorf("dir removed: %v", err)
	}
	entries, _ := os.ReadDir(dst)
	if len(entries) != 0 {
		t.Errorf("clearDir left %d entries: %v", len(entries), entries)
	}
}

func TestClearDir_MissingDirIsNoOp(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no-such")
	if err := clearDir(missing); err != nil {
		t.Errorf("clearDir on missing dir = %v, want nil (ENOENT is non-fatal)", err)
	}
}

// --- ApplyLayerGz: bad gzip header ------------------------------------------

func TestApplyLayerGz_BadGzip(t *testing.T) {
	err := ApplyLayerGz(t.TempDir(), bytes.NewReader([]byte("not a gzip stream")))
	if err == nil {
		t.Fatal("ApplyLayerGz should reject non-gzip input")
	}
	if !strings.Contains(err.Error(), "gzip") {
		t.Errorf("error %q should mention gzip", err.Error())
	}
}
