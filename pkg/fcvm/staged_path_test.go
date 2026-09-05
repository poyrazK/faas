package fcvm

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestStagedDrivePath_OptimizedArtifactUsesUpper(t *testing.T) {
	root := t.TempDir()
	got, err := stagedDrivePath(root, "upper/etc/faas/env.json")
	if err != nil {
		t.Fatalf("stagedDrivePath: %v", err)
	}
	want := filepath.Join(root, "upper", "etc", "faas", "env.json")
	if got != want {
		t.Fatalf("path = %q, want %q", got, want)
	}
}

func TestStagedDrivePath_FullRootfsUsesRoot(t *testing.T) {
	root := t.TempDir()
	marker := filepath.Join(root, filepath.FromSlash("etc/faas/.full-rootfs"))
	if err := os.MkdirAll(filepath.Dir(marker), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(marker, []byte(api.FullRootfsMarkerValue), 0o444); err != nil {
		t.Fatal(err)
	}

	got, err := stagedDrivePath(root, "upper/etc/faas/env.json")
	if err != nil {
		t.Fatalf("stagedDrivePath: %v", err)
	}
	want := filepath.Join(root, "etc", "faas", "env.json")
	if got != want {
		t.Fatalf("path = %q, want %q", got, want)
	}
}

func TestStagedDrivePath_RejectsInvalidMarker(t *testing.T) {
	root := t.TempDir()
	marker := filepath.Join(root, filepath.FromSlash("etc/faas/.full-rootfs"))
	if err := os.MkdirAll(filepath.Dir(marker), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(marker, []byte("customer-controlled\n"), 0o444); err != nil {
		t.Fatal(err)
	}
	if _, err := stagedDrivePath(root, "upper/etc/faas/env.json"); err == nil {
		t.Fatal("stagedDrivePath accepted an invalid full-rootfs marker")
	}
}

func TestStagedDrivePath_RejectsMarkerSymlink(t *testing.T) {
	root := t.TempDir()
	marker := filepath.Join(root, filepath.FromSlash("etc/faas/.full-rootfs"))
	if err := os.MkdirAll(filepath.Dir(marker), 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "marker")
	if err := os.WriteFile(target, []byte(api.FullRootfsMarkerValue), 0o444); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, marker); err != nil {
		t.Fatal(err)
	}
	if _, err := stagedDrivePath(root, "upper/etc/faas/env.json"); err == nil {
		t.Fatal("stagedDrivePath accepted a marker symlink")
	}
}
