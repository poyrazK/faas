//go:build linux

// adr: 567 — private drives have no unowned named materialisation window.
package fcvm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// The journal holds the original prepared VM lock throughout this operation.
// Anonymous, close-on-exec output cannot outlive daemon death before an owned
// anchor exists. After that, the source epoch owns the inode and both binds.
func (b linuxNativeImageSources) PrepareWritable(ctx context.Context, owner nativeLaunchRecord, root, source, name string) (prepared nativeImagePreparation, err error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if owner.Authorized || owner.Revoked || owner.ResourcesRemoved || owner.Lease.IsBuilder || name != layerImageName {
		return nil, errors.New("native image source: private clone requires an original prepared app drive")
	}
	input, err := b.Prepare(owner, root, source, name, false)
	if err != nil {
		return nil, err
	}
	p := input.(*linuxNativeImagePreparation)
	defer func() {
		if prepared == nil {
			err = errors.Join(err, p.Close())
		}
	}()
	clone, err := cloneNativeImage(ctx, p.source, filepath.Dir(source))
	if err != nil {
		return nil, err
	}
	previous := p.source
	p.source = clone
	p.identity, _, err = nativeImageFileMetadata(clone)
	if err = errors.Join(err, previous.Close()); err != nil {
		return nil, err
	}
	p.link = false
	return p, nil
}

func nativeCloneFilesystemSupported(kind int64) bool {
	switch kind {
	case unix.EXT4_SUPER_MAGIC, unix.XFS_SUPER_MAGIC, unix.BTRFS_SUPER_MAGIC:
		return true
	default:
		return false
	}
}

func cloneNativeImage(ctx context.Context, source *os.File, directory string) (output *os.File, err error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	identity, _, err := nativeImageFileMetadata(source)
	if err != nil {
		return nil, err
	}
	info, err := source.Stat()
	if err != nil {
		return nil, err
	}
	if info.Size() <= 0 || info.Size() == math.MaxInt64 {
		return nil, errors.New("native image source: private image has no bounded positive size")
	}
	parent, err := os.OpenFile(directory, os.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := parent.Close(); closeErr != nil {
			err = errors.Join(err, closeErr)
			if output != nil {
				err = errors.Join(err, output.Close())
				output = nil
			}
		}
	}()
	var stat unix.Stat_t
	var filesystem unix.Statfs_t
	if err := errors.Join(unix.Fstat(int(parent.Fd()), &stat), unix.Fstatfs(int(parent.Fd()), &filesystem)); err != nil {
		return nil, err
	}
	if uint64(stat.Dev) != identity.Device || !nativeCloneFilesystemSupported(filesystem.Type) {
		return nil, errors.New("native image source: clone requires the original ext4, XFS or Btrfs disk filesystem")
	}
	if err := source.Sync(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	fd, err := unix.Openat(int(parent.Fd()), ".", unix.O_TMPFILE|unix.O_EXCL|unix.O_RDWR|unix.O_CLOEXEC, 0o600)
	if err != nil {
		return nil, fmt.Errorf("native image source: anonymous clone creation: %w", err)
	}
	clone := os.NewFile(uintptr(fd), "native-private-image")
	defer func() {
		if output == nil {
			err = errors.Join(err, clone.Close())
		}
	}()
	if err := clone.Chmod(0o600); err != nil {
		return nil, err
	}
	if err := populateNativeImageClone(ctx, source, clone, info.Size(), unix.IoctlFileClone); err != nil {
		return nil, err
	}
	cloneIdentity, _, err := nativeImageFileMetadata(clone)
	if err != nil || cloneIdentity == identity || cloneIdentity.Device != identity.Device {
		return nil, errors.Join(err, errors.New("native image source: clone aliases or differs from original filesystem"))
	}
	return clone, nil
}

func populateNativeImageClone(ctx context.Context, source, clone *os.File, size int64, reflink func(int, int) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := reflink(int(clone.Fd()), int(source.Fd())); err != nil {
		if !errors.Is(err, unix.EOPNOTSUPP) && !errors.Is(err, unix.ENOTTY) && !errors.Is(err, unix.EINVAL) && !errors.Is(err, unix.EXDEV) && !errors.Is(err, unix.ENOSYS) {
			return fmt.Errorf("native image source: reflink private image: %w", err)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := clone.Truncate(0); err != nil {
			return err
		}
		if _, err := clone.Seek(0, io.SeekStart); err != nil {
			return err
		}
		if _, err := source.Seek(0, io.SeekStart); err != nil {
			return err
		}
		reader := io.LimitReader(nativeImageCopyReader{ctx: ctx, source: source}, size+1)
		if copied, err := io.CopyBuffer(clone, reader, make([]byte, 128*1024)); err != nil || copied != size {
			return errors.Join(err, errors.New("native image source: private image copy was incomplete or changed size"))
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	original, sourceErr := source.Stat()
	copied, cloneErr := clone.Stat()
	if err := errors.Join(sourceErr, cloneErr); err != nil {
		return err
	}
	if original.Size() != size || copied.Size() != size {
		return errors.New("native image source: original or private clone changed size")
	}
	if err := clone.Sync(); err != nil {
		return err
	}
	return ctx.Err()
}

type nativeImageCopyReader struct {
	ctx    context.Context
	source io.Reader
}

func (r nativeImageCopyReader) Read(body []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := r.source.Read(body)
	if canceled := r.ctx.Err(); canceled != nil {
		return 0, canceled
	}
	return n, err
}
