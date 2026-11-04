package rootfs

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// Host file operations on a staging tree after layer extraction.
//
// Image layers keep symlinks verbatim, absolute targets included (see
// applyEntry). A plain filepath.Join(staging, ...) followed by os.Open,
// os.WriteFile, os.Stat or os.MkdirAll would follow such a link out of the
// staging tree: an image shipping etc/passwd -> /etc/faas/secrets/fleet.age
// would have imaged read a host secret, and one shipping
// etc/faas -> /srv/fc/base would have it overwrite shared host files. Every
// operation on a guest path after extraction goes through these helpers,
// which resolve the path the way the guest will (absolute link targets
// re-anchored at the staging root, ".." clamped there) and never follow a
// link in the final component of a write.

// stagingPath resolves guestPath fully, following every symlink the way the
// guest kernel will, clamped inside staging. Use it to read or inspect an
// existing guest file.
func stagingPath(staging, guestPath string) (string, error) {
	rel := strings.TrimPrefix(filepath.ToSlash(guestPath), "/")
	if _, err := safeJoin(staging, rel); err != nil {
		return "", err
	}
	resolved, err := resolveWithin(staging, rel)
	if err != nil {
		return "", fmt.Errorf("rootfs: resolve %q: %w", guestPath, err)
	}
	return resolved, nil
}

// stagingEntryPath resolves guestPath's ancestors, clamped inside staging,
// and keeps its final component unresolved. Use it for an operation that
// replaces or renames the entry itself.
func stagingEntryPath(staging, guestPath string) (string, error) {
	return resolveEntryPath(staging, strings.TrimPrefix(filepath.ToSlash(guestPath), "/"))
}

// lstatStaging reports what guestPath resolves to inside staging. A missing
// file, or a symlink whose clamped target is missing, wraps os.ErrNotExist.
func lstatStaging(staging, guestPath string) (string, os.FileInfo, error) {
	p, err := stagingPath(staging, guestPath)
	if err != nil {
		return "", nil, err
	}
	info, err := os.Lstat(p)
	if err != nil {
		return p, nil, err
	}
	return p, info, nil
}

// openStagingFile opens the regular file guestPath resolves to inside
// staging. Anything else (a directory, a device) is refused rather than
// opened, and the final component is opened without following links.
func openStagingFile(staging, guestPath string) (*os.File, os.FileInfo, error) {
	p, info, err := lstatStaging(staging, guestPath)
	if err != nil {
		return nil, nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, nil, fmt.Errorf("rootfs: %s is not a regular file", guestPath)
	}
	// p is resolved inside the builder-owned staging tree; O_NOFOLLOW keeps a
	// link swapped in after the lstat from being followed.
	f, err := os.OpenFile(p, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, nil, err
	}
	return f, info, nil
}

// readStagingFile reads the regular file guestPath resolves to inside
// staging.
func readStagingFile(staging, guestPath string) ([]byte, os.FileInfo, error) {
	f, info, err := openStagingFile(staging, guestPath)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(f)
	if err != nil {
		return nil, nil, err
	}
	return data, info, nil
}

// writeStagingFile replaces the entry at guestPath with a regular file
// holding data and mode, creating missing parent directories. A symlink or
// file already there is removed first, so the write lands inside staging
// even when the image put a link at that path. It returns the host path
// written.
func writeStagingFile(staging, guestPath string, data []byte, mode os.FileMode) (string, error) {
	p, err := stagingEntryPath(staging, guestPath)
	if err != nil {
		return "", err
	}
	if err := mkdirImageDirectories(filepath.Dir(p), 0o755); err != nil {
		return "", err
	}
	if err := removeStagingEntry(p); err != nil {
		return "", err
	}
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, mode)
	if err != nil {
		return "", err
	}
	_, writeErr := f.Write(data)
	if err := errors.Join(writeErr, f.Chmod(mode), f.Close()); err != nil {
		return "", err
	}
	return p, nil
}

// renameStagingEntry moves the entry at fromGuest to toGuest. Neither final
// component is followed, so a symlink moves as a link.
func renameStagingEntry(staging, fromGuest, toGuest string) error {
	from, err := stagingEntryPath(staging, fromGuest)
	if err != nil {
		return err
	}
	to, err := stagingEntryPath(staging, toGuest)
	if err != nil {
		return err
	}
	return os.Rename(from, to)
}

// mkdirStaging creates guestPath and its missing parents, following existing
// symlinks the way the guest will, clamped inside staging.
func mkdirStaging(staging, guestPath string, mode os.FileMode) error {
	p, err := stagingPath(staging, guestPath)
	if err != nil {
		return err
	}
	return mkdirImageDirectories(p, mode)
}

// removeStagingEntry removes a file or symlink at p, which must already be
// resolved inside staging. A directory is refused, and a missing entry is
// not an error.
func removeStagingEntry(p string) error {
	info, err := os.Lstat(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.IsDir() {
		return fmt.Errorf("rootfs: %s is a directory", p)
	}
	return os.Remove(p)
}
