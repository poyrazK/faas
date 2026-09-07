//go:build linux

package rootfs

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

var (
	overlayMknod    = unix.Mknod
	overlaySetxattr = unix.Setxattr
)

// applyOverlayWhiteout records an OCI whiteout in the form expected by
// overlayfs: a character device with major/minor 0/0 in the upper directory.
// The victim is removed from the staging tree first because it may have been
// introduced by an earlier app layer; the marker then hides the same name from
// the shared base drive after guest-init mounts the upper filesystem.
func applyOverlayWhiteout(parent, victimName, marker string) error {
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return fmt.Errorf("create parent: %w", err)
	}
	victim := filepath.Join(parent, victimName)
	if err := os.RemoveAll(victim); err != nil {
		return fmt.Errorf("remove upper victim: %w", err)
	}
	if err := os.RemoveAll(marker); err != nil {
		return fmt.Errorf("replace existing marker: %w", err)
	}
	// A whiteout is metadata, so keep it inaccessible even if an artifact is
	// accidentally inspected outside an overlay mount.
	if err := overlayMknod(marker, unix.S_IFCHR|0o600, int(unix.Mkdev(0, 0))); err != nil {
		return fmt.Errorf("create overlay character device: %w", err)
	}
	return nil
}

// applyOverlayOpaque clears entries already materialized in the upper tree
// and marks the directory opaque. Overlayfs consumes the xattr when it merges
// this directory with drive0. trusted.overlay.opaque is the normal kernel
// form; user.overlay.opaque is a fallback for hosts that expose only user xattrs
// while constructing the ext4 source tree.
func applyOverlayOpaque(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create directory: %w", err)
	}
	if err := clearDir(dir); err != nil {
		return fmt.Errorf("clear directory: %w", err)
	}
	if err := overlaySetxattr(dir, "trusted.overlay.opaque", []byte("y"), 0); err == nil {
		return nil
	} else if err := overlaySetxattr(dir, "user.overlay.opaque", []byte("y"), 0); err != nil {
		return fmt.Errorf("set overlay opaque xattr: %w", err)
	}
	return nil
}
