package scanview

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPinnedProjectionStaysInOriginalOutputAndConfinesGuestLinks(t *testing.T) {
	source, target, foreign := t.TempDir(), t.TempDir(), t.TempDir()
	if err := os.Mkdir(filepath.Join(source, "etc"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "etc", "package"), []byte("guest bytes"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc/package", filepath.Join(source, "absolute")); err != nil {
		t.Fatal(err)
	}
	expected, err := Snapshot(t.Context(), source)
	if err != nil {
		t.Fatal(err)
	}
	pinned, err := os.OpenRoot(target)
	if err != nil {
		t.Fatal(err)
	}
	defer pinned.Close()
	if err := os.Rename(target, target+"-old"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(target + "-old") })
	if err := os.Symlink(foreign, target); err != nil {
		t.Fatal(err)
	}
	actual, err := CopyToRoot(t.Context(), source, pinned, expected)
	if err != nil || actual.ProjectionDigest != expected.ProjectionDigest {
		t.Fatal("pinned projection changed guest content", err)
	}
	if data, err := pinned.ReadFile("absolute"); err != nil || string(data) != "guest bytes" {
		t.Fatal("projection link escaped guest root", err)
	}
	if entries, err := os.ReadDir(foreign); err != nil || len(entries) != 0 {
		t.Fatal("projection followed replacement output path", err)
	}
}
