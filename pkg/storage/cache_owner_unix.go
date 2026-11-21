//go:build unix

package storage

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// lockEvictionOwner takes an exclusive advisory lock on the owner file and
// holds it until release.
func lockEvictionOwner(root string) (func(), error) {
	f, err := os.OpenFile(filepath.Join(root, cacheEvictionOwnerFile), os.O_CREATE|os.O_RDWR, 0o644) //nolint:forbidigo // fixed name under the cache root.
	if err != nil {
		return nil, fmt.Errorf("storage: open cache eviction owner lock: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("storage: cache eviction already owned: %w", err)
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}

// evictionOwnerHeld probes the owner lock without waiting: true while another
// open file description holds it.
func evictionOwnerHeld(root string) bool {
	f, err := os.Open(filepath.Join(root, cacheEvictionOwnerFile)) //nolint:forbidigo // fixed name under the cache root.
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	err = syscall.Flock(int(f.Fd()), syscall.LOCK_SH|syscall.LOCK_NB)
	if err == nil {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		return false
	}
	return errors.Is(err, syscall.EWOULDBLOCK)
}
