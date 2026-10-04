//go:build linux

// adr: 568 — native frozen drives never acquire a persistent output pathname.
package fcvm

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"

	"golang.org/x/sys/unix"
)

func (linuxNativeImageSources) FreezeSnapshotDrive(ctx context.Context, input *os.File, directory string) (frozen *os.File, err error) {
	if input == nil || !filepath.IsAbs(directory) || filepath.Clean(directory) != directory || directory == "/" {
		return nil, errors.New("native snapshot output: pinned input and original disk directory are required")
	}
	clone, err := cloneNativeImage(ctx, input, directory)
	if err != nil {
		return nil, err
	}
	defer func() {
		// The sole writable output handle closes before a read-only handle
		// transfers to the consumer, including on close failure.
		err = errors.Join(err, clone.Close())
		if err != nil && frozen != nil {
			err = errors.Join(err, frozen.Close())
			frozen = nil
		}
	}()
	if err := clone.Chmod(0o400); err != nil {
		return nil, err
	}
	if err := clone.Sync(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// This procfs path names our still-open, verified anonymous FD. Reopening
	// gives the consumer a new read-only description at offset zero; neither
	// the old image path nor a newly named output is used.
	fd, err := unix.Open(nativeImageFDPath(clone), unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	// Some synchronous storage consumers reopen File.Name(). Name the new
	// description itself, never the writable FD that is about to close.
	frozen = os.NewFile(uintptr(fd), "/proc/self/fd/"+strconv.Itoa(fd))
	if err := checkNativeFrozenSnapshotDrive(input, frozen); err != nil {
		return frozen, err
	}
	return frozen, ctx.Err()
}
