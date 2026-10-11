//go:build linux

// adr: 568 — memory/device outputs bind anonymous disk inodes before capture.
package fcvm

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

func (b linuxNativeImageSources) PrepareSnapshotOutput(ctx context.Context, owner nativeLaunchRecord, root, directory, name string) (prepared nativeImagePreparation, err error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	kind, capture := "", ""
	if strings.HasSuffix(name, "-mem") {
		kind, capture = "mem", strings.TrimSuffix(strings.TrimPrefix(name, "capture-"), "-mem")
	} else if strings.HasSuffix(name, "-vmstate") {
		kind, capture = "vmstate", strings.TrimSuffix(strings.TrimPrefix(name, "capture-"), "-vmstate")
	}
	expected, nameErr := nativeSnapshotOutputName(capture, kind)
	if nameErr != nil || expected != name || owner.Authorized || owner.Revoked || owner.ExitConfirmed || owner.ResourcesRemoved || owner.Lease.IsBuilder || !filepath.IsAbs(directory) || filepath.Clean(directory) != directory || directory == "/" {
		return nil, errors.New("native snapshot output: original output preparation and disk placement are required")
	}
	rootFile, _, err := b.prepareRoot(owner, root, name)
	if err != nil {
		return nil, err
	}
	defer func() {
		if prepared == nil {
			err = errors.Join(err, rootFile.Close())
		}
	}()
	namespace, err := nativeLoopNamespaceIdentity()
	if err != nil {
		return nil, err
	}
	output, err := createNativeSnapshotOutput(ctx, directory)
	if err != nil {
		return nil, err
	}
	identity, _, statErr := nativeImageFileMetadata(output)
	if statErr != nil {
		return nil, errors.Join(statErr, output.Close())
	}
	return &linuxNativeImagePreparation{source: output, root: rootFile, identity: identity, namespace: namespace, owner: owner}, nil
}

func createNativeSnapshotOutput(ctx context.Context, directory string) (output *os.File, err error) {
	parent, err := os.OpenFile(directory, os.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	defer func() {
		err = errors.Join(err, parent.Close())
		if err != nil && output != nil {
			err = errors.Join(err, output.Close())
			output = nil
		}
	}()
	var filesystem unix.Statfs_t
	if err := unix.Fstatfs(int(parent.Fd()), &filesystem); err != nil {
		return nil, err
	}
	if !nativeCloneFilesystemSupported(filesystem.Type) {
		return nil, errors.New("native snapshot output: disk filesystem is not qualified for anonymous outputs")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	fd, err := unix.Openat(int(parent.Fd()), ".", unix.O_TMPFILE|unix.O_EXCL|unix.O_RDWR|unix.O_CLOEXEC, 0o600)
	if err != nil {
		return nil, err
	}
	output = os.NewFile(uintptr(fd), "native-capture-output")
	err = errors.Join(output.Chmod(0o600), output.Sync(), ctx.Err())
	return output, err
}

// Read-only preflight: no output is created before placement is proven against
// the retained private-drive inode. TMPDIR must be its disk filesystem.
func (linuxNativeImageSources) CheckSnapshotDirectory(ctx context.Context, input *os.File, directory string) (err error) {
	if input == nil || !filepath.IsAbs(directory) || filepath.Clean(directory) != directory || directory == "/" {
		return errors.New("native snapshot: original input and canonical disk directory are required")
	}
	if err := checkNativeEffectDirectory(directory); err != nil {
		return err
	}
	parent, err := os.OpenFile(directory, os.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, parent.Close()) }()
	var drive, dir unix.Stat_t
	var filesystem unix.Statfs_t
	if err := errors.Join(unix.Fstat(int(input.Fd()), &drive), unix.Fstat(int(parent.Fd()), &dir), unix.Fstatfs(int(parent.Fd()), &filesystem)); err != nil {
		return err
	}
	if drive.Dev != dir.Dev || !nativeCloneFilesystemSupported(filesystem.Type) || dir.Uid != uint32(os.Geteuid()) || dir.Mode&0o022 != 0 {
		return errors.New("native snapshot: output directory differs from original protected disk filesystem")
	}
	return ctx.Err()
}
