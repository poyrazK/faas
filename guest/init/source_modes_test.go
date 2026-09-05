package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBuildSourceModesPreserveExecutablesAndSkipSymlinks(t *testing.T) {
	root := t.TempDir()
	for _, file := range []struct {
		name string
		mode os.FileMode
		want os.FileMode
	}{{"build.sh", 0o755, 0o777}, {"package.json", 0o644, 0o666}, {"private-tool", 0o700, 0o766}} {
		path := filepath.Join(root, file.name)
		if err := os.WriteFile(path, []byte("source"), file.mode); err != nil {
			t.Fatal(err)
		}
	}
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("untouched"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "external-link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("missing", filepath.Join(root, "dangling-link")); err != nil {
		t.Fatal(err)
	}
	if err := prepareBuildSourceModes(root); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]os.FileMode{"build.sh": 0o777, "package.json": 0o666, "private-tool": 0o766} {
		info, err := os.Stat(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != want {
			t.Errorf("%s: mode=%o want=%o", name, info.Mode().Perm(), want)
		}
	}
	info, err := os.Stat(outside)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("symlink target changed to %o", info.Mode().Perm())
	}
}
