//go:build linux

package rootfs

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"

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
func applyOverlayWhiteout(parent, victimName string) error {
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return fmt.Errorf("create parent: %w", err)
	}
	victim := filepath.Join(parent, victimName)
	if err := os.RemoveAll(victim); err != nil {
		return fmt.Errorf("remove upper victim: %w", err)
	}
	// A whiteout is metadata, so keep it inaccessible even if an artifact is
	// accidentally inspected outside an overlay mount.
	if err := overlayMknod(victim, unix.S_IFCHR|0o600, int(unix.Mkdev(0, 0))); err != nil {
		return fmt.Errorf("create overlay character device: %w", err)
	}
	return nil
}

// applyOverlayOpaque clears entries already materialized in the upper tree
// and marks the directory opaque. Overlayfs consumes the xattr when it merges
// this directory with drive0. trusted.overlay.opaque is the normal kernel
// form used by guest-init, which does not mount with userxattr. Refuse when
// the current owner cannot create that metadata; another namespace is not proof.
func applyOverlayOpaque(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create directory: %w", err)
	}
	if err := clearDir(dir); err != nil {
		return fmt.Errorf("clear directory: %w", err)
	}
	if err := overlaySetxattr(dir, "trusted.overlay.opaque", []byte("y"), 0); err != nil {
		return fmt.Errorf("set guest overlay opaque xattr: %w", err)
	}
	return nil
}

// Recreating a deleted directory must not expose its previously deleted lower
// children. Ancestor traversal uses this before creating implicit directories.
func replaceOverlayWhiteout(target string, directory bool) error {
	info, err := os.Lstat(target)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeCharDevice == 0 {
		return nil
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Rdev != 0 {
		return nil
	}
	if err := os.Remove(target); err != nil {
		return err
	}
	if directory {
		return applyOverlayOpaque(target)
	}
	return nil
}
