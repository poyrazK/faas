// Targeted tests for the remaining pkg/rootfs branches: safeJoin's traversal
// guard, applyEntry's hardlink + char-device + symlink branches, clearDir's
// missing-dir path, and ApplyLayerGz's bad-gzip path. The happy-path
// ApplyLayer/safeJoin cases are already covered by rootfs_test.go; this file
// pins down the negative paths.

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

// --- safeJoin ---------------------------------------------------------------

func TestSafeJoin(t *testing.T) {
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
			got, err := safeJoin(tc.base, tc.entry)
			if tc.wantErr {
				if err == nil {
					t.Errorf("safeJoin(%q, %q) = %q, want error", tc.base, tc.entry, got)
				}
				return
			}
			if err != nil {
				t.Errorf("safeJoin(%q, %q) error: %v", tc.base, tc.entry, err)
				return
			}
			// Defence-in-depth: result must be under base.
			rel, relErr := filepath.Rel(tc.base, got)
			if relErr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				t.Errorf("safeJoin result %q escaped base %q (rel=%q)", got, tc.base, rel)
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

func TestApplyEntry_Symlink(t *testing.T) {
	dst := t.TempDir()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	if err := tw.WriteHeader(&tar.Header{
		Name: "link", Linkname: "/usr/bin/env", Typeflag: tar.TypeSymlink, Mode: 0o777,
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
	if target != "/usr/bin/env" {
		t.Errorf("symlink target = %q, want /usr/bin/env", target)
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
