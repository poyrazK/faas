//go:build linux

package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureGuestHostsFile(t *testing.T) {
	t.Run("created when the image has none", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "etc", "hosts")
		if err := ensureGuestHostsFile(path, "gregale"); err != nil {
			t.Fatal(err)
		}
		assertHostsFile(t, path, "127.0.0.1\tlocalhost\n10.0.0.2\tgregale\n")
	})
	t.Run("image entries kept", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "hosts")
		writeHostsFixture(t, path, "10.1.2.3 db.internal\n")
		if err := ensureGuestHostsFile(path, "gregale"); err != nil {
			t.Fatal(err)
		}
		assertHostsFile(t, path, "10.1.2.3 db.internal\n127.0.0.1\tlocalhost\n10.0.0.2\tgregale\n")
	})
	t.Run("complete file untouched", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "hosts")
		writeHostsFixture(t, path, "127.0.0.1 localhost\n10.0.0.2 gregale\n")
		if err := os.Chmod(path, 0o444); err != nil {
			t.Fatal(err)
		}
		if err := ensureGuestHostsFile(path, "gregale"); err != nil {
			t.Fatal(err)
		}
		assertHostsFile(t, path, "127.0.0.1 localhost\n10.0.0.2 gregale\n")
	})
	t.Run("dangling symlink replaced", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "hosts")
		if err := os.Symlink(filepath.Join(dir, "missing", "hosts"), path); err != nil {
			t.Fatal(err)
		}
		if err := ensureGuestHostsFile(path, "gregale"); err != nil {
			t.Fatal(err)
		}
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() {
			t.Fatalf("hosts is not a regular file: %v %v", info, err)
		}
		assertHostsFile(t, path, "127.0.0.1\tlocalhost\n10.0.0.2\tgregale\n")
	})
}

func writeHostsFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func assertHostsFile(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatalf("%s = %q, want %q", path, got, want)
	}
}
