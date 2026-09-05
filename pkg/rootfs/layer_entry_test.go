// Direct exercise of the unexported applyEntry/clearDir so we get branch
// coverage on the per-entry-handler switch (regular/dir/symlink/hardlink/
// unsupported type) and on the whiteout + clearDir helpers without depending
// on integration paths. Public ApplyLayer tests live in rootfs_test.go.
package rootfs

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestApplyEntry_TypeDir(t *testing.T) {
	dir := t.TempDir()
	hdr := &tar.Header{Name: "sub", Mode: 0o755, Typeflag: tar.TypeDir}
	if err := applyEntry(dir, filepath.Join(dir, "sub"), hdr, nil, nil); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(filepath.Join(dir, "sub"))
	if err != nil {
		t.Fatal(err)
	}
	if !st.IsDir() {
		t.Errorf("sub is not a directory")
	}
}

func TestApplyEntry_TypeRegTruncatesExisting(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "f")
	if err := os.WriteFile(target, []byte("OLD"), 0o644); err != nil {
		t.Fatal(err)
	}
	hdr := &tar.Header{Name: "f", Mode: 0o644, Typeflag: tar.TypeReg, Size: 2}
	if err := applyEntry(dir, target, hdr, strings.NewReader("OK"), nil); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(target)
	if string(got) != "OK" {
		t.Errorf("body = %q, want OK", got)
	}
}

func TestApplyEntry_TypeRegReplacesExistingSymlink(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(dir, "outside")
	if err := os.WriteFile(outside, []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "bin", "dmesg")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, target); err != nil {
		t.Fatal(err)
	}

	hdr := &tar.Header{Name: "bin/dmesg", Mode: 0o755, Typeflag: tar.TypeReg, Size: 3}
	if err := applyEntry(dir, target, hdr, strings.NewReader("new"), nil); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new" {
		t.Errorf("body = %q, want new", got)
	}
	if info, err := os.Lstat(target); err != nil {
		t.Fatal(err)
	} else if info.Mode()&os.ModeSymlink != 0 {
		t.Fatal("regular layer entry left the previous symlink in place")
	}
	if got, err := os.ReadFile(outside); err != nil {
		t.Fatal(err)
	} else if string(got) != "outside" {
		t.Errorf("symlink target was modified: %q", got)
	}
}

func TestApplyEntry_SymlinkAbsoluteTargetIsVerbatim(t *testing.T) {
	// A header whose Linkname is absolute is written VERBATIM — the string
	// is guest-side data, resolved by the guest kernel once the ext4 is
	// mounted at "/". Absolute targets are ubiquitous in real OCI layers
	// (alpine: 306 of them), so rejecting them here bricked imaged.
	//
	// The host-escape concern is handled on the WRITE path instead:
	// resolveEntryPath/resolveWithin clamp ancestor symlinks inside the
	// staging root, so this link cannot be used to reach the host's /etc.
	// See TestApplyLayer_WriteThroughSymlinkIsClamped in safety_test.go.
	dir := t.TempDir()
	target := filepath.Join(dir, "link")
	hdr := &tar.Header{Name: "link", Typeflag: tar.TypeSymlink, Linkname: "/etc/hostname"}
	if err := applyEntry(dir, target, hdr, nil, nil); err != nil {
		t.Fatalf("applyEntry rejected an absolute symlink target: %v", err)
	}
	got, err := os.Readlink(target)
	if err != nil {
		t.Fatalf("symlink not created: %v", err)
	}
	if got != "/etc/hostname" {
		t.Errorf("link text = %q, want %q (verbatim)", got, "/etc/hostname")
	}
}

func TestApplyEntry_HardlinkResolvesRelativeToBase(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "real"), []byte("payload"), 0o644); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "alias")
	// Linkname relative to the archive root (== base in applyEntry).
	hdr := &tar.Header{Name: "alias", Typeflag: tar.TypeLink, Linkname: "real"}
	if err := applyEntry(dir, target, hdr, nil, nil); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if !st.Mode().IsRegular() {
		t.Errorf("alias is not a regular file (mode=%v)", st.Mode())
	}
	got, _ := os.ReadFile(target)
	if string(got) != "payload" {
		t.Errorf("body = %q, want payload", got)
	}
}

func TestApplyEntry_HardlinkRejectsPathEscape(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "evil")
	hdr := &tar.Header{Name: "evil", Typeflag: tar.TypeLink, Linkname: "../../etc/passwd"}
	if err := applyEntry(dir, target, hdr, nil, nil); err == nil {
		t.Fatal("expected path-escape rejection on hardlink linkname")
	}
}

func TestApplyEntry_UnsupportedTypeReturnsNil(t *testing.T) {
	// Char/block/fifo entries are skipped, not errored — caller continues.
	dir := t.TempDir()
	target := filepath.Join(dir, "dev")
	hdr := &tar.Header{Name: "dev", Typeflag: tar.TypeChar}
	if err := applyEntry(dir, target, hdr, nil, nil); err != nil {
		t.Errorf("char entry should be skipped, got %v", err)
	}
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		t.Errorf("char entry should not be created, got lstat err %v", err)
	}
}

func TestApplyEntry_TypeRegBadPathReturnsWrapped(t *testing.T) {
	// Force MkdirAll on a path under a file-as-directory to surface the
	// wrapped mkdir error.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "block"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "block", "nested", "file")
	hdr := &tar.Header{Name: "block/nested/file", Mode: 0o644, Typeflag: tar.TypeReg, Size: 1}
	if err := applyEntry(dir, target, hdr, bytes.NewReader([]byte("x")), nil); err == nil {
		t.Fatal("expected error when parent is a regular file")
	}
}

func TestClearDir_RemovesChildrenKeepsDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sub", "b"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := clearDir(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Errorf("clearDir removed the parent: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "a")); !os.IsNotExist(err) {
		t.Errorf("a should be gone, stat err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "sub", "b")); !os.IsNotExist(err) {
		t.Errorf("sub/b should be gone, stat err = %v", err)
	}
}

func TestClearDir_MissingDirIsNoop(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope")
	if err := clearDir(missing); err != nil {
		t.Errorf("missing dir should be a noop, got %v", err)
	}
}

func TestApplyLayer_FailsOnNextError(t *testing.T) {
	// A truncated tar (header for an entry that is broken) forces ApplyLayer
	// to surface a non-EOF error from tr.Next().
	dir := t.TempDir()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	// Drive the tar header on one goroutine so we can withhold bytes and
	// close the pipe without tripping tar.Writer.Close()'s size check.
	go func() {
		defer w.Close()
		tw := tar.NewWriter(w)
		hdr := &tar.Header{Name: "file", Mode: 0o644, Typeflag: tar.TypeReg, Size: 4}
		if err := tw.WriteHeader(hdr); err != nil {
			return
		}
		// Only 2 of the declared 4 bytes — ApplyLayer will hit io.ErrUnexpectedEOF
		// (or similar) at io.CopyN.
		_, _ = tw.Write([]byte("ab"))
		// Don't call Close() — that would itself fail and obscure the test
		// goal; instead let the pipe drop the writer when this fn returns.
	}()

	if err := ApplyLayer(dir, tar.NewReader(r)); err == nil {
		t.Fatal("expected error from truncated tar entry")
	}
}
