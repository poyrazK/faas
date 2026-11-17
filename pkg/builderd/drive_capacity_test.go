//go:build linux

package builderd

// adr: 20261029103512 — the developer dependency cache yields its bytes before
// a build drive is refused.

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckBuildDriveCapacityReclaimsDependencyCacheFirst(t *testing.T) {
	driveDir := t.TempDir()
	entry := filepath.Join(driveDir, devDependencyCacheDir, strings.Repeat("c", 64))
	if err := os.MkdirAll(entry, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(entry, "layer"), make([]byte, 1024), 0o600); err != nil {
		t.Fatal(err)
	}
	previous := driveFilesystemAvailable
	t.Cleanup(func() { driveFilesystemAvailable = previous })
	// The filesystem has room only once the cache entry is gone.
	driveFilesystemAvailable = func(string) (uint64, error) {
		if _, err := os.Stat(entry); err == nil {
			return BuildDriveMinFreeBytes - 1, nil
		}
		return BuildDriveMinFreeBytes, nil
	}
	if err := checkBuildDriveCapacity(filepath.Join(driveDir, "build.ext4")); err != nil {
		t.Fatalf("capacity after reclaim: %v", err)
	}
	if _, err := os.Stat(entry); !os.IsNotExist(err) {
		t.Fatalf("dependency cache entry was not reclaimed: %v", err)
	}

	driveFilesystemAvailable = func(string) (uint64, error) { return BuildDriveMinFreeBytes - 1, nil }
	if err := checkBuildDriveCapacity(filepath.Join(driveDir, "build.ext4")); !errors.Is(err, ErrBuildDiskFull) {
		t.Fatalf("full disk = %v, want ErrBuildDiskFull", err)
	}
}

// adr: 20261030122204 — a BuildKit seed yields its bytes before a build drive
// is refused.
func TestCheckBuildDriveCapacityDropsBuildKitSeedsFirst(t *testing.T) {
	root := t.TempDir()
	driveDir := filepath.Join(root, "drive")
	if err := os.MkdirAll(driveDir, 0o750); err != nil {
		t.Fatal(err)
	}
	s := &buildkitSeedStore{dir: buildkitSeedDir(driveDir)}
	if err := os.MkdirAll(s.dir, 0o750); err != nil {
		t.Fatal(err)
	}
	writeFile(t, s.seedPath("k"), "seed")
	writeFile(t, s.readyPath("k"), "ready")
	previous := driveFilesystemAvailable
	t.Cleanup(func() { driveFilesystemAvailable = previous })
	// The filesystem has room only once the seed is gone.
	driveFilesystemAvailable = func(string) (uint64, error) {
		if _, err := os.Stat(s.seedPath("k")); err == nil {
			return BuildDriveMinFreeBytes - 1, nil
		}
		return BuildDriveMinFreeBytes, nil
	}
	if err := checkBuildDriveCapacity(filepath.Join(driveDir, "build.ext4")); err != nil {
		t.Fatalf("capacity after dropping the seed: %v", err)
	}
	if _, ok := s.ready("k"); ok {
		t.Fatal("the seed survived a build that needed its space")
	}
}
