//go:build linux

package rootfs

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestApplyLayerWithOverlayWhiteoutsCreatesUpperMarker(t *testing.T) {
	oldMknod := overlayMknod
	defer func() { overlayMknod = oldMknod }()
	var gotPath string
	overlayMknod = func(path string, mode uint32, dev int) error {
		gotPath = path
		return os.WriteFile(path, nil, 0o600)
	}

	dst := t.TempDir()
	victim := filepath.Join(dst, "base-only")
	if err := os.WriteFile(victim, []byte("upper copy"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ApplyLayerWithOverlayWhiteouts(dst, tar.NewReader(singleTar(t, &tar.Header{Name: ".wh.base-only"}))); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(victim); !os.IsNotExist(err) {
		t.Fatalf("upper victim still exists: %v", err)
	}
	wantMarker := filepath.Join(dst, ".wh.base-only")
	if gotPath != wantMarker {
		t.Fatalf("mknod path = %q, want %q", gotPath, wantMarker)
	}
	if _, err := os.Stat(wantMarker); err != nil {
		t.Fatalf("whiteout marker missing: %v", err)
	}
	if err := ApplyLayerWithOverlayWhiteouts(dst, tar.NewReader(singleTar(t, &tar.Header{
		Name: "base-only", Mode: 0o644,
	}))); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(wantMarker); !os.IsNotExist(err) {
		t.Fatalf("replacement retained whiteout marker: %v", err)
	}
}

func TestApplyOverlayOpaqueClearsAndMarksDirectory(t *testing.T) {
	oldSetxattr := overlaySetxattr
	defer func() { overlaySetxattr = oldSetxattr }()
	var names []string
	overlaySetxattr = func(path, name string, value []byte, flags int) error {
		names = append(names, name)
		return nil
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "old"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := applyOverlayOpaque(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "old")); !os.IsNotExist(err) {
		t.Fatalf("opaque directory retained old child: %v", err)
	}
	if len(names) != 1 || names[0] != "trusted.overlay.opaque" {
		t.Fatalf("xattr calls = %v, want trusted.overlay.opaque", names)
	}
}

func singleTar(t *testing.T, hdr *tar.Header) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return &buf
}
