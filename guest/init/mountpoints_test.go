package main

import (
	"os"
	"path/filepath"
	"testing"
)

// A minimal image ships none of the mountpoints; all must be created.
func TestEnsureMountpoints_CreatesMissingDirs(t *testing.T) {
	root := t.TempDir()
	if err := ensureMountpoints(root, "/dev", "/proc", "/sys"); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{"dev", "proc", "sys"} {
		info, err := os.Stat(filepath.Join(root, d))
		if err != nil || !info.IsDir() {
			t.Errorf("%s: err=%v isDir=%v; a devtmpfs mount onto a missing /dev fails with ENOENT "+
				"and the guest has no /dev/null", d, err, err == nil && info.IsDir())
		}
	}
}

// An image that ships its own /dev directory is left exactly as it is.
func TestEnsureMountpoints_KeepsExistingDirs(t *testing.T) {
	root := t.TempDir()
	dev := filepath.Join(root, "dev")
	if err := os.MkdirAll(dev, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dev, "marker"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ensureMountpoints(root, "/dev"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dev, "marker")); err != nil {
		t.Errorf("existing /dev was disturbed: %v", err)
	}
}

// A regular file in the way is an image defect worth naming, not something to
// silently mount over.
func TestEnsureMountpoints_RejectsNonDirectory(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "dev"), []byte("not a dir"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ensureMountpoints(root, "/dev"); err == nil {
		t.Fatal("a file at /dev was accepted; the devtmpfs mount would fail with ENOTDIR and no explanation")
	}
}
