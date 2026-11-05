//go:build linux

// adr: 20261022184951 — native runtime snapshots live in a tree only vmmd writes.
package storage

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"strings"

	"golang.org/x/sys/unix"
)

// CheckPrivateExclusiveRoot reports whether root can hold objects that only
// this process's uid may change: a real directory it owns, with no group-write
// bit and no access at all for others, on a filesystem the exclusive writer
// supports. Group read is allowed so collectors can inspect the tree.
func CheckPrivateExclusiveRoot(root string) error {
	fd, err := unix.Openat2(unix.AT_FDCWD, root, &unix.OpenHow{
		Flags:   unix.O_RDONLY | unix.O_DIRECTORY | unix.O_NOFOLLOW | unix.O_CLOEXEC,
		Resolve: unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS,
	})
	if err != nil {
		return fmt.Errorf("storage: open private root %q: %w", root, err)
	}
	directory := os.NewFile(uintptr(fd), "private-exclusive-root")
	defer func() { _ = directory.Close() }()
	var metadata unix.Stat_t
	if err := unix.Fstat(int(directory.Fd()), &metadata); err != nil {
		return err
	}
	if metadata.Mode&0o007 != 0 {
		return fmt.Errorf("storage: private root %q must give others no access", root)
	}
	return checkExclusiveArtifactDirectory(directory)
}

// DeleteExclusive removes key from a tree that only this process writes,
// without following links, and then removes the directories it leaves empty
// strictly below keep, which must be an ancestor directory of key. Directories
// at or above keep are shared with other objects being published, so they are
// never removed. A missing object is success, as for Delete. Every directory
// on the path must still pass the exclusive writer's ownership check, so a
// tree another principal can write is refused.
func (l *LocalStorageBackend) DeleteExclusive(ctx context.Context, key, keep string) error {
	if err := validateKey(key); err != nil {
		return err
	}
	if err := validateKey(keep); err != nil || !strings.HasPrefix(key, keep+"/") {
		return fmt.Errorf("%w: %q is not an ancestor of %q", ErrInvalidKey, keep, key)
	}
	parent, found, err := l.exclusiveExistingParent(ctx, key)
	if err != nil || !found {
		return err
	}
	name := key[strings.LastIndex(key, "/")+1:]
	err = unix.Unlinkat(int(parent.Fd()), name, 0)
	if errors.Is(err, unix.ENOENT) {
		err = nil
	}
	err = errors.Join(err, parent.Close())
	if err != nil {
		return fmt.Errorf("storage: exclusive delete %q: %w", key, err)
	}
	for directory := path.Dir(key); strings.HasPrefix(directory, keep+"/"); directory = path.Dir(directory) {
		removed, err := l.removeEmptyExclusiveDirectory(ctx, directory)
		if err != nil {
			return fmt.Errorf("storage: exclusive delete %q: prune %q: %w", key, directory, err)
		}
		if !removed {
			break
		}
	}
	return nil
}

// exclusiveExistingParent opens key's parent directory as exclusiveParent
// does, but reports a missing directory on the way instead of returning an
// ancestor in its place.
func (l *LocalStorageBackend) exclusiveExistingParent(ctx context.Context, key string) (parent *os.File, found bool, err error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	root, err := unix.Openat2(unix.AT_FDCWD, l.root, &unix.OpenHow{
		Flags:   unix.O_RDONLY | unix.O_DIRECTORY | unix.O_NOFOLLOW | unix.O_CLOEXEC,
		Resolve: unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS,
	})
	if err != nil {
		return nil, false, errors.Join(ErrExclusivePutUnsupported, err)
	}
	parent = os.NewFile(uintptr(root), "exclusive-artifact-root")
	defer func() {
		if err != nil || !found {
			err = errors.Join(err, parent.Close())
			parent = nil
		}
	}()
	if err := checkExclusiveArtifactDirectory(parent); err != nil {
		return parent, false, err
	}
	parts := strings.Split(key, "/")
	for _, part := range parts[:len(parts)-1] {
		if err := ctx.Err(); err != nil {
			return parent, false, err
		}
		fd, openErr := openExclusiveArtifactDirectory(parent, part)
		if errors.Is(openErr, unix.ENOENT) {
			return parent, false, nil
		}
		if openErr != nil {
			return parent, false, openErr
		}
		child := os.NewFile(uintptr(fd), "exclusive-artifact-directory")
		if err := parent.Close(); err != nil {
			parent = child
			return parent, false, err
		}
		parent = child
		if err := checkExclusiveArtifactDirectory(parent); err != nil {
			return parent, false, err
		}
	}
	return parent, true, nil
}

func (l *LocalStorageBackend) removeEmptyExclusiveDirectory(ctx context.Context, directory string) (bool, error) {
	parent, found, err := l.exclusiveExistingParent(ctx, directory)
	if err != nil || !found {
		return !found && err == nil, err
	}
	defer func() { _ = parent.Close() }()
	err = unix.Unlinkat(int(parent.Fd()), path.Base(directory), unix.AT_REMOVEDIR)
	switch {
	case err == nil:
		return true, parent.Sync()
	case errors.Is(err, unix.ENOENT):
		return true, nil
	case errors.Is(err, unix.ENOTEMPTY), errors.Is(err, unix.EEXIST):
		return false, nil
	default:
		return false, err
	}
}
