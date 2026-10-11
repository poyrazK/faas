//go:build linux

// adr: 568 — local receipts verify opened bytes without granting unlink rights.
package storage

import (
	"context"
	"errors"
	"io"
	"os"
	"path"

	"golang.org/x/sys/unix"
)

func (l *LocalStorageBackend) GetExclusiveArtifact(ctx context.Context, r ExclusiveArtifactReceipt) (reader io.ReadCloser, err error) {
	defer func() {
		if err != nil && reader != nil {
			err = errors.Join(err, reader.Close())
			reader = nil
		}
	}()
	if err := errors.Join(r.Validate(), ctx.Err()); err != nil {
		return nil, err
	}
	if r.Backend != "local" || r.Location != l.root || r.Key != r.ObjectKey {
		return nil, ErrArtifactReceiptMismatch
	}
	parent, err := l.exclusiveParent(ctx, r.ObjectKey, false)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, parent.Close()) }()
	root, err := l.exclusiveParent(ctx, "root-probe", false)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	var parentStat, rootStat unix.Stat_t
	if err := errors.Join(unix.Fstat(int(parent.Fd()), &parentStat), unix.Fstat(int(root.Fd()), &rootStat)); err != nil {
		return nil, err
	}
	identity := r.Local
	if uint64(parentStat.Dev) != identity.ParentDevice || parentStat.Ino != identity.ParentInode || uint64(rootStat.Dev) != identity.RootDevice || rootStat.Ino != identity.RootInode {
		return nil, ErrArtifactReceiptMismatch
	}
	fd, err := unix.Openat2(int(parent.Fd()), path.Base(r.ObjectKey), &unix.OpenHow{Flags: unix.O_RDONLY | unix.O_NONBLOCK | unix.O_NOFOLLOW | unix.O_CLOEXEC, Resolve: unix.RESOLVE_BENEATH | unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS | unix.RESOLVE_NO_XDEV})
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), "original-exclusive-artifact")
	defer func() {
		if err != nil {
			err = errors.Join(err, file.Close())
			reader = nil
		}
	}()
	var observed unix.Stat_t
	if err := unix.Fstat(fd, &observed); err != nil {
		return nil, err
	}
	if uint64(observed.Dev) != identity.Device || observed.Ino != identity.Inode || observed.Mode&unix.S_IFMT != unix.S_IFREG || observed.Mode&0o7777 != 0o644 || observed.Uid != uint32(os.Geteuid()) || observed.Size != r.LogicalBytes || observed.Nlink != 1 {
		return nil, ErrArtifactReceiptMismatch
	}
	// Allocation is a commit-time observation; do not require it to remain fixed
	// after filesystem compression or deduplication. Content digest is verified
	// by GetExclusiveArtifact as the original opened reader reaches EOF.
	if err := errors.Join(l.checkExclusiveParentPlacement(ctx, r.ObjectKey, parent), ctx.Err()); err != nil {
		return nil, err
	}
	return file, nil
}
