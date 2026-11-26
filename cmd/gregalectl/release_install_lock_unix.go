//go:build !windows

package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// Variables so tests can shorten them.
var (
	releaseInstallLockWait = 15 * time.Minute
	releaseInstallLockPoll = time.Second
)

// lockReleaseInstalls guards the staging area (ADR-20261123150000).
// Preparation's background --stage-only can outlive a failed prepare play; a
// rerun or the activation that follows then waits for it instead of working
// on the same staged tree at the same time.
func lockReleaseInstalls(dir string) (func(), error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("release install lock: create %s: %w", dir, err)
	}
	path := filepath.Join(dir, releaseInstallLockName)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("release install lock: open %s: %w", path, err)
	}
	// Bounded: a hung holder must not keep a drained node's activation
	// waiting forever. node_join caps the stage unit at RuntimeMaxSec=840, so
	// a healthy wait ends well inside this deadline.
	deadline := time.Now().Add(releaseInstallLockWait)
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			_ = f.Close()
			return nil, fmt.Errorf("release install lock: flock %s: %w", path, err)
		}
		if time.Now().After(deadline) {
			_ = f.Close()
			return nil, fmt.Errorf("release install lock: %s still held after %s by another release install (a stuck --stage-only?)", path, releaseInstallLockWait)
		}
		time.Sleep(releaseInstallLockPoll)
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}
