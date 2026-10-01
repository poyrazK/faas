package main

import (
	"fmt"
	"os"
	"path/filepath"
)

// devtmpfs supplies devices, but not the userspace descriptor symlinks that
// OCI processes commonly use. Each target resolves against the caller's proc
// view, including a sidecar's chroot. Never follow or replace an unexpected
// image-supplied file at one of the reserved paths.
func ensureGuestFDLinks(devDir string) error {
	for _, link := range []struct{ name, target string }{
		{"fd", "/proc/self/fd"}, {"stdin", "/proc/self/fd/0"},
		{"stdout", "/proc/self/fd/1"}, {"stderr", "/proc/self/fd/2"},
	} {
		path := filepath.Join(devDir, link.name)
		info, err := os.Lstat(path)
		if err == nil {
			if info.Mode()&os.ModeSymlink != 0 {
				if target, readErr := os.Readlink(path); readErr == nil && target == link.target {
					continue
				}
			}
			return fmt.Errorf("guest device descriptor %s has an unexpected entry", link.name)
		}
		if !os.IsNotExist(err) {
			return fmt.Errorf("inspect guest descriptor %s: %w", link.name, err)
		}
		if err := os.Symlink(link.target, path); err != nil {
			return fmt.Errorf("create guest descriptor %s: %w", link.name, err)
		}
	}
	return nil
}
