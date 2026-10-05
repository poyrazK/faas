//go:build linux

// adr: 568 — memory/device outputs bind anonymous disk inodes before capture.
package fcvm

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
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
	if err := b.requireAnonymousStagingFilesystem(directory); err != nil {
		return nil, err
	}
	rootFile, err := b.prepareCaptureOutputRoot(ctx, owner, root, name)
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
	return &linuxNativeImagePreparation{source: output, root: rootFile, identity: identity, namespace: namespace, owner: owner, diskRoot: b.diskStagingRoot}, nil
}

// Boot gives the jail root to the Firecracker UID so it can create its API
// socket. Live capture cannot borrow ordinary prepared-root authority, nor may
// it chown the directory back. Match the actual original process's chroot while
// a pidfd is pinned, then retain that directory FD through output preparation.
func (b linuxNativeImageSources) prepareCaptureOutputRoot(ctx context.Context, owner nativeLaunchRecord, root, name string) (file *os.File, err error) {
	if err := nativeImageRootPlacement(b.base, owner, root, name); err != nil {
		return nil, err
	}
	file, err = os.OpenFile(root, os.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil && file != nil {
			err = errors.Join(err, file.Close())
			file = nil
		}
	}()
	var directory unix.Stat_t
	if err := unix.Fstat(int(file.Fd()), &directory); err != nil {
		return file, err
	}
	if directory.Uid == uint32(os.Geteuid()) {
		// Prepared-root primitive fixtures keep their existing contract. The
		// complete live producer carries capture authority regardless.
		if err := file.Close(); err != nil {
			return file, err
		}
		file, _, err = b.prepareRoot(owner, root, name)
		return file, err
	}
	permit, ok := ctx.Value(nativeSnapshotCaptureContextKey{}).(nativeSnapshotCapturePermit)
	if !ok || !sameNativePhysicalLease(owner.Lease, permit.Physical.Lease) || owner.Generation != permit.Physical.Generation ||
		owner.KernelBootID != permit.Physical.KernelBootID || !liveNativeSnapshotOwner(permit.Physical) ||
		directory.Uid != uint32(owner.Lease.UID) || directory.Gid != uint32(owner.Lease.GID) || directory.Mode&0o022 != 0 {
		return file, errors.New("native snapshot output: live jail lacks original process and UID ownership")
	}
	if err := permit.Capture.validate(permit.Incoming); err != nil {
		return file, err
	}
	if !permit.Capture.CompletedAt.IsZero() {
		return file, errors.New("native snapshot output: completed capture cannot prepare another output")
	}
	handle, err := openNativeProcess(permit.Physical.PID)
	if err != nil {
		return file, err
	}
	defer func() { err = errors.Join(err, handle.Close()) }()
	if err := checkNativeSnapshotProcess(handle.(*nativePIDFD), permit.Physical); err != nil {
		return file, err
	}
	processRoot, err := os.OpenFile("/proc/"+strconv.Itoa(permit.Physical.PID)+"/root", os.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return file, err
	}
	var original unix.Stat_t
	if err := errors.Join(unix.Fstat(int(processRoot.Fd()), &original), processRoot.Close()); err != nil {
		return file, err
	}
	if original.Dev != directory.Dev || original.Ino != directory.Ino {
		return file, errors.New("native snapshot output: jail differs from the original process root")
	}
	// The marker was created by the prepared owner. Never create or replace a
	// marker while the jail is writable by an already-live process.
	if err := checkNativeImageRoot(root, owner); err != nil {
		return file, err
	}
	if _, err := os.Lstat(filepath.Join(root, name)); !errors.Is(err, os.ErrNotExist) {
		return file, errors.Join(err, errors.New("native snapshot output: original destination already exists"))
	}
	return file, errors.Join(checkNativeSnapshotProcess(handle.(*nativePIDFD), permit.Physical), ctx.Err())
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
	fd, err := unix.Openat(int(parent.Fd()), ".", unix.O_TMPFILE|unix.O_RDWR|unix.O_CLOEXEC, 0o600)
	if err != nil {
		return nil, err
	}
	output = os.NewFile(uintptr(fd), "native-capture-output")
	err = errors.Join(output.Chmod(0o600), output.Sync(), ctx.Err())
	return output, err
}
