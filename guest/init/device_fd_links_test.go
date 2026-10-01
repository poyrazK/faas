package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGuestDescriptorLinks(t *testing.T) {
	dev := t.TempDir()
	for i := 0; i < 2; i++ {
		if err := ensureGuestFDLinks(dev); err != nil {
			t.Fatal(err)
		}
	}
	for name, target := range map[string]string{"fd": "/proc/self/fd", "stdin": "/proc/self/fd/0", "stdout": "/proc/self/fd/1", "stderr": "/proc/self/fd/2"} {
		got, err := os.Readlink(filepath.Join(dev, name))
		if err != nil || got != target {
			t.Fatalf("%s=%q/%v, want %s", name, got, err, target)
		}
	}
}

func TestGuestDescriptorLinksRejectUnexpectedEntry(t *testing.T) {
	for _, kind := range []string{"file", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			dev := t.TempDir()
			p := filepath.Join(dev, "stdout")
			if kind == "file" {
				if err := os.WriteFile(p, []byte("unchanged"), 0600); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.Symlink("/etc/passwd", p); err != nil {
					t.Fatal(err)
				}
			}
			if err := ensureGuestFDLinks(dev); err == nil {
				t.Fatal("accepted unexpected entry")
			}
			if kind == "file" {
				if data, err := os.ReadFile(p); err != nil || string(data) != "unchanged" {
					t.Fatalf("overwrote file: %s/%v", data, err)
				}
			} else {
				if target, _ := os.Readlink(p); target != "/etc/passwd" {
					t.Fatal("replaced unexpected link")
				}
			}
		})
	}
}
