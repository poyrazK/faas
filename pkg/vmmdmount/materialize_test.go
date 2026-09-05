package vmmdmount

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestCopyParentTreeCopiesContentsIntoTarget(t *testing.T) {
	source := t.TempDir()
	target := t.TempDir()
	if err := os.MkdirAll(filepath.Join(source, "usr", "bin"), 0o755); err != nil {
		t.Fatalf("mkdir source: %v", err)
	}
	const payload = "parent-file"
	if err := os.WriteFile(filepath.Join(source, "usr", "bin", "node"), []byte(payload), 0o755); err != nil {
		t.Fatalf("write source: %v", err)
	}

	if err := copyParentTree(context.Background(), source, target); err != nil {
		t.Fatalf("copyParentTree: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(target, "usr", "bin", "node"))
	if err != nil {
		t.Fatalf("read copied file: %v", err)
	}
	if string(got) != payload {
		t.Fatalf("copied payload = %q, want %q", got, payload)
	}
	if _, err := os.Stat(filepath.Join(target, filepath.Base(source), "usr", "bin", "node")); !os.IsNotExist(err) {
		t.Fatalf("parent directory was nested under target: err=%v", err)
	}
}

func TestCopyParentTreePreservesStagingRootMode(t *testing.T) {
	source := t.TempDir()
	target := t.TempDir()
	if err := os.Chmod(source, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(target, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, ".parent-marker"), []byte("parent"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(".parent-marker", filepath.Join(source, "link")); err != nil {
		t.Fatal(err)
	}
	if err := copyParentTree(context.Background(), source, target); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("staging root mode = %o, want 0700", info.Mode().Perm())
	}
	info, err = os.Stat(filepath.Join(target, ".parent-marker"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Fatalf("child mode = %o, want 0640", info.Mode().Perm())
	}
	link, err := os.Readlink(filepath.Join(target, "link"))
	if err != nil || link != ".parent-marker" {
		t.Fatalf("copied symlink = %q, err=%v", link, err)
	}
}

func TestCopyParentTreeEmptyParent(t *testing.T) {
	source, target := t.TempDir(), t.TempDir()
	if err := copyParentTree(context.Background(), source, target); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(target)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("empty parent produced %d entries", len(entries))
	}
}
