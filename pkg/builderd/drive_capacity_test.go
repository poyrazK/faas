//go:build linux

package builderd

// adr: 20261028090000 — the developer dependency cache yields its bytes before
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
	previous := buildDriveAvailable
	t.Cleanup(func() { buildDriveAvailable = previous })
	// The filesystem has room only once the cache entry is gone.
	buildDriveAvailable = func(string) (uint64, error) {
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

	buildDriveAvailable = func(string) (uint64, error) { return BuildDriveMinFreeBytes - 1, nil }
	if err := checkBuildDriveCapacity(filepath.Join(driveDir, "build.ext4")); !errors.Is(err, ErrBuildDiskFull) {
		t.Fatalf("full disk = %v, want ErrBuildDiskFull", err)
	}
}
