package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureStandardDevLinksCreatesContainerLinks(t *testing.T) {
	dev := t.TempDir()
	if err := ensureStandardDevLinks(dev); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"fd": "/proc/self/fd", "stdin": "/proc/self/fd/0", "stdout": "/proc/self/fd/1", "stderr": "/proc/self/fd/2"} {
		got, err := os.Readlink(filepath.Join(dev, name))
		if err != nil || got != want {
			t.Errorf("/dev/%s -> %q (%v), want %q", name, got, err, want)
		}
	}
	// Idempotent across restarts of the stage.
	if err := ensureStandardDevLinks(dev); err != nil {
		t.Fatalf("second pass: %v", err)
	}
}

func TestEnsureStandardDevLinksKeepsImageEntries(t *testing.T) {
	dev := t.TempDir()
	if err := os.Symlink("/dev/console", filepath.Join(dev, "stdout")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dev, "stdin"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ensureStandardDevLinks(dev); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.Readlink(filepath.Join(dev, "stdout")); got != "/dev/console" {
		t.Errorf("replaced the image's /dev/stdout link: %q", got)
	}
	if info, err := os.Lstat(filepath.Join(dev, "stdin")); err != nil || info.Mode()&os.ModeSymlink != 0 {
		t.Errorf("replaced the image's /dev/stdin entry: %v %v", info, err)
	}
	if got, _ := os.Readlink(filepath.Join(dev, "stderr")); got != "/proc/self/fd/2" {
		t.Errorf("/dev/stderr -> %q, want the standard link", got)
	}
}
