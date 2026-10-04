//go:build linux || darwin

// adr: 567 — frozen drive outputs remain inside original native ownership.
package fcvm

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// This output capability alone grants no pause, publication or capture proof.
// The capture adapter must freeze while paused, resume only after freezing,
// and publish through its separately fenced original object namespace.
type nativeSnapshotFrozenDriveBackend interface {
	FreezeSnapshotDrive(context.Context, *os.File, string) (*os.File, error)
}

// The consumer must finish synchronously. Both the frozen output and original
// input close before the source and physical locks release. No output pathname
// or descriptor may escape this boundary.
func (v *JailerVMM) withNativeFrozenSnapshotDrive(ctx context.Context, lease Lease, directory string, consume func(*os.File) error) error {
	r := v.nativeRecovery
	if r == nil || consume == nil || !filepath.IsAbs(directory) || filepath.Clean(directory) != directory || directory == "/" {
		return errors.New("native snapshot output: original owner, private disk directory and consumer are required")
	}
	backend, ok := r.imageSources.(nativeSnapshotFrozenDriveBackend)
	if !ok {
		return errors.New("native snapshot output: anonymous frozen-drive producer is unavailable")
	}
	return v.withNativeSnapshotDriveInput(ctx, lease, func(input *os.File) (err error) {
		output, err := backend.FreezeSnapshotDrive(ctx, input, directory)
		if err != nil {
			if output != nil {
				err = errors.Join(err, output.Close())
			}
			return err
		}
		if output == nil {
			return errors.New("native snapshot output: producer returned no frozen descriptor")
		}
		defer func() { err = errors.Join(err, output.Close()) }()
		if err := checkNativeFrozenSnapshotDrive(input, output); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		return errors.Join(consume(output), ctx.Err())
	})
}

func checkNativeFrozenSnapshotDrive(input, output *os.File) error {
	var original, frozen unix.Stat_t
	if err := errors.Join(unix.Fstat(int(input.Fd()), &original), unix.Fstat(int(output.Fd()), &frozen)); err != nil {
		return err
	}
	if frozen.Mode&unix.S_IFMT != unix.S_IFREG || frozen.Mode&0o7777 != 0o400 || frozen.Uid != uint32(os.Geteuid()) || frozen.Nlink != 0 || frozen.Size <= 0 || frozen.Size != original.Size || frozen.Dev != original.Dev || frozen.Ino == original.Ino {
		return errors.New("native snapshot output: frozen descriptor lacks anonymous private-copy identity")
	}
	access, accessErr := unix.FcntlInt(output.Fd(), unix.F_GETFL, 0)
	flags, flagsErr := unix.FcntlInt(output.Fd(), unix.F_GETFD, 0)
	if err := errors.Join(accessErr, flagsErr); err != nil {
		return err
	}
	if access&unix.O_ACCMODE != unix.O_RDONLY || flags&unix.FD_CLOEXEC == 0 {
		return errors.New("native snapshot output: frozen descriptor is writable or inheritable")
	}
	// O_PATH reports read-only access bits but cannot read bytes. Verify the
	// read capability without changing the consumer's offset.
	if _, err := output.ReadAt(make([]byte, 1), 0); err != nil {
		return errors.Join(err, errors.New("native snapshot output: frozen descriptor cannot read data"))
	}
	return nil
}
