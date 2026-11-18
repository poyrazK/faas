package builderd

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// unreadableFile makes a file builderd cannot open, the way a seed drive
// left owned by a seed run's jail uid looks to it (2026-11-17).
func unreadableFile(t *testing.T, path string) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("root reads a mode 0000 file")
	}
	writeFile(t, path, "seed")
	if err := os.Chmod(path, 0); err != nil {
		t.Fatal(err)
	}
}

func TestBuildKitSeedStoreRefusesAnUnreadableSeed(t *testing.T) {
	root := t.TempDir()
	s := &buildkitSeedStore{dir: filepath.Join(root, "buildkit-seed")}
	drive := filepath.Join(root, "build-seed-buildkit.ext4")
	unreadableFile(t, drive)
	err := s.install("key", drive)
	if err == nil || !strings.Contains(err.Error(), "not readable") {
		t.Fatalf("install of an unreadable seed = %v, want a refusal", err)
	}
	if _, ok := s.ready("key"); ok {
		t.Fatal("an unreadable seed was installed")
	}
}

func TestBuildKitSeedStoreDropsASeedItCannotCopy(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("cloneFile is a Linux reflink")
	}
	root := t.TempDir()
	s := &buildkitSeedStore{dir: filepath.Join(root, "buildkit-seed")}
	if err := os.MkdirAll(s.dir, 0o750); err != nil {
		t.Fatal(err)
	}
	unreadableFile(t, s.seedPath("key"))
	writeFile(t, s.readyPath("key"), "ready")
	dst := filepath.Join(root, "copy.ext4")
	if err := s.copyTo("key", dst); !errorsIsPermission(err) {
		t.Fatalf("copyTo = %v, want a permission error", err)
	}
	if _, ok := s.ready("key"); ok {
		t.Fatal("a seed builderd cannot read is still ready")
	}
	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		t.Fatalf("a failed copy left %s: %v", dst, err)
	}
}

func errorsIsPermission(err error) bool {
	return err != nil && (os.IsPermission(err) || strings.Contains(err.Error(), fs.ErrPermission.Error()))
}
