//go:build linux

// adr: 568 — local capture publication names only the completed immutable file.
package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

func (l *LocalStorageBackend) CheckExclusivePut(ctx context.Context, key string) (err error) {
	if err := validateKey(key); err != nil {
		return err
	}
	parent, err := l.exclusiveParent(ctx, key, false)
	if err != nil {
		return err
	}
	return parent.Close()
}

func (l *LocalStorageBackend) PutExclusive(ctx context.Context, key string, reader io.Reader, size int64) error {
	_, err := l.PutExclusiveArtifact(ctx, key, reader, size)
	return err
}

func (l *LocalStorageBackend) CheckExclusiveArtifact(ctx context.Context, key string) error {
	return l.CheckExclusivePut(ctx, key)
}

func (l *LocalStorageBackend) PutExclusiveArtifact(ctx context.Context, key string, reader io.Reader, size int64) (receipt ExclusiveArtifactReceipt, err error) {
	defer func() {
		if err != nil {
			receipt = ExclusiveArtifactReceipt{}
		}
	}()
	if err := validateKey(key); err != nil {
		return receipt, err
	}
	if reader == nil || size <= 0 {
		return receipt, errors.New("storage: exclusive publication requires a nonempty original reader")
	}
	parent, err := l.exclusiveParent(ctx, key, true)
	if err != nil {
		return receipt, err
	}
	defer func() { err = errors.Join(err, parent.Close()) }()
	root, err := l.exclusiveParent(ctx, "root-probe", false)
	if err != nil {
		return receipt, err
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	fd, err := unix.Openat(int(parent.Fd()), ".", unix.O_TMPFILE|unix.O_RDWR|unix.O_CLOEXEC, 0o600)
	if err != nil {
		return receipt, fmt.Errorf("storage: exclusive put %q: anonymous output: %w", key, err)
	}
	output := os.NewFile(uintptr(fd), "exclusive-artifact")
	defer func() { err = errors.Join(err, output.Close()) }()
	digest := sha256.New()
	source := &exactArtifactReader{source: io.TeeReader(reader, digest), remaining: size}
	written, err := copySparseArtifactContext(ctx, output, source)
	if err != nil || !source.complete || written != size {
		return receipt, errors.Join(err, errors.New("storage: exclusive copy did not finish its original source"))
	}
	if err := errors.Join(ctx.Err(), output.Chmod(0o644), output.Sync()); err != nil {
		return receipt, err
	}
	if err := l.checkExclusiveParentPlacement(ctx, key, parent); err != nil {
		return receipt, err
	}
	parts := strings.Split(key, "/")
	// Link only the original still-open anonymous file, without replacement.
	if err := unix.Linkat(unix.AT_FDCWD, "/proc/self/fd/"+strconv.Itoa(fd), int(parent.Fd()), parts[len(parts)-1], unix.AT_SYMLINK_FOLLOW); err != nil {
		if errors.Is(err, unix.EEXIST) {
			err = errors.Join(ErrArtifactExists, err)
		}
		return receipt, err
	}
	// Post-link uncertainty retains the object and supplies no receipt. Metadata
	// is read from the original output descriptor, never from a later key lookup.
	if err := errors.Join(parent.Sync(), ctx.Err(), l.checkExclusiveParentPlacement(ctx, key, parent)); err != nil {
		return receipt, err
	}
	var artifact, rootStat, parentStat unix.Stat_t
	if err := errors.Join(unix.Fstat(fd, &artifact), unix.Fstat(int(root.Fd()), &rootStat), unix.Fstat(int(parent.Fd()), &parentStat)); err != nil {
		return receipt, err
	}
	if artifact.Nlink != 1 || artifact.Size != size || artifact.Blocks < 0 {
		return receipt, ErrArtifactReceiptMismatch
	}
	receipt = ExclusiveArtifactReceipt{Version: 1, Key: key, ObjectKey: key, Backend: "local", Location: l.root, LogicalBytes: size, StoredBytes: artifact.Blocks * 512, SHA256: hex.EncodeToString(digest.Sum(nil)), Local: &ExclusiveLocalReceipt{Device: uint64(artifact.Dev), Inode: artifact.Ino, RootDevice: uint64(rootStat.Dev), RootInode: rootStat.Ino, ParentDevice: uint64(parentStat.Dev), ParentInode: parentStat.Ino}}
	return receipt, receipt.Validate()
}

func (l *LocalStorageBackend) exclusiveParent(ctx context.Context, key string, create bool) (parent *os.File, err error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	root, err := unix.Openat2(unix.AT_FDCWD, l.root, &unix.OpenHow{
		Flags:   unix.O_RDONLY | unix.O_DIRECTORY | unix.O_NOFOLLOW | unix.O_CLOEXEC,
		Resolve: unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS,
	})
	if err != nil {
		return nil, errors.Join(ErrExclusivePutUnsupported, err)
	}
	parent = os.NewFile(uintptr(root), "exclusive-artifact-root")
	defer func() {
		if err != nil {
			err = errors.Join(err, parent.Close())
			parent = nil
		}
	}()
	if err := checkExclusiveArtifactDirectory(parent); err != nil {
		return parent, err
	}
	parts := strings.Split(key, "/")
	for _, part := range parts[:len(parts)-1] {
		if err := ctx.Err(); err != nil {
			return parent, err
		}
		fd, openErr := openExclusiveArtifactDirectory(parent, part)
		if errors.Is(openErr, unix.ENOENT) {
			if !create {
				return parent, nil // The remaining canonical path is not created by preflight.
			}
			if err := unix.Mkdirat(int(parent.Fd()), part, 0o755); err != nil && !errors.Is(err, unix.EEXIST) {
				return parent, err
			}
			if err := parent.Sync(); err != nil {
				return parent, err
			}
			fd, openErr = openExclusiveArtifactDirectory(parent, part)
		}
		if openErr != nil {
			return parent, openErr
		}
		child := os.NewFile(uintptr(fd), "exclusive-artifact-directory")
		if err := parent.Close(); err != nil {
			return child, err
		}
		parent = child
		if err := checkExclusiveArtifactDirectory(parent); err != nil {
			return parent, err
		}
	}
	return parent, nil
}

func openExclusiveArtifactDirectory(parent *os.File, name string) (int, error) {
	return unix.Openat2(int(parent.Fd()), name, &unix.OpenHow{
		Flags:   unix.O_RDONLY | unix.O_DIRECTORY | unix.O_NOFOLLOW | unix.O_CLOEXEC,
		Resolve: unix.RESOLVE_BENEATH | unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS | unix.RESOLVE_NO_XDEV,
	})
}

func checkExclusiveArtifactDirectory(directory *os.File) error {
	var metadata unix.Stat_t
	var filesystem unix.Statfs_t
	if err := errors.Join(unix.Fstat(int(directory.Fd()), &metadata), unix.Fstatfs(int(directory.Fd()), &filesystem)); err != nil {
		return err
	}
	if metadata.Mode&unix.S_IFMT != unix.S_IFDIR || metadata.Uid != uint32(os.Geteuid()) || metadata.Mode&0o022 != 0 {
		return errors.New("storage: exclusive publication requires an original owned directory without shared write permission")
	}
	if filesystem.Type != unix.EXT4_SUPER_MAGIC && filesystem.Type != unix.XFS_SUPER_MAGIC && filesystem.Type != unix.BTRFS_SUPER_MAGIC {
		return ErrExclusivePutUnsupported
	}
	return nil
}

func (l *LocalStorageBackend) checkExclusiveParentPlacement(ctx context.Context, key string, expected *os.File) (err error) {
	current, err := l.exclusiveParent(ctx, key, false)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, current.Close()) }()
	var original, observed unix.Stat_t
	if err := errors.Join(unix.Fstat(int(expected.Fd()), &original), unix.Fstat(int(current.Fd()), &observed)); err != nil {
		return err
	}
	if original.Dev != observed.Dev || original.Ino != observed.Ino {
		return errors.New("storage: exclusive destination placement changed before publication")
	}
	return ctx.Err()
}
