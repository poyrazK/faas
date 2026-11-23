//go:build !windows

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// lockReleaseInstalls serializes `release install` runs on one host
// (ADR-20261123150000). Preparation's background --stage-only can outlive a
// failed prepare play; a rerun or the activation that follows then waits for
// it instead of extracting into, and marking, the same release directory at
// the same time. The lock file is not a release id, so retention ignores it.
func lockReleaseInstalls(releasesRoot string) (func(), error) {
	if err := os.MkdirAll(releasesRoot, 0o755); err != nil {
		return nil, fmt.Errorf("release install lock: create %s: %w", releasesRoot, err)
	}
	path := filepath.Join(releasesRoot, ".install.lock")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("release install lock: open %s: %w", path, err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("release install lock: flock %s: %w", path, err)
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}
