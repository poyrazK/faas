//go:build linux

// adr: 568 — verified backing copies retain captured names and target ownership.
package fcvm

import (
	"context"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

type nativeSnapshotRestoreBackingBackend interface {
	PrepareRestoreBacking(context.Context, nativeLaunchRecord, string, *os.File, nativeSnapshotBackingImage) (nativeImagePreparation, error)
}

// Paths provide only candidate bytes. The original capture's private journal
// supplies every name, digest and size. Both copies are anonymous, sealed and
// verified before any target epoch, mount or permission grant. This internal
// operation still grants no launch/load/readiness or canonical key fallback.
func (v *JailerVMM) stageNativeQualificationRestoreBackings(ctx context.Context, owner nativeLaunchRecord, paths [2]string) (result error) {
	r := v.nativeRecovery
	if r == nil || r.journal == nil || v.nativeImageStagingRoot == "" {
		return errors.New("native qualification restore: original backing staging runtime is unavailable")
	}
	backend, ok := r.imageSources.(nativeSnapshotRestoreBackingBackend)
	if !ok {
		return errors.New("native qualification restore: verified backing staging adapter is unavailable")
	}
	lock, producer, err := r.journal.lockQualificationProducer(ctx, owner.Lease.Instance)
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, lock.Close()) }()
	if producer == nil || producer.restore == nil || producer.NativeGeneration != owner.Generation || !sameNativePhysicalLease(producer.NativeLease, owner.Lease) {
		return errors.New("native qualification restore: original target backing authority is unavailable")
	}
	ctx, cancel := context.WithDeadline(ctx, producer.restore.Deadline)
	defer cancel()
	if err := v.requireNativeRestoreStagingOwner(owner); err != nil {
		return err
	}
	q := r.journal.qualifications(producer.Execution.NodeID)
	capture, err := q.restores().requireCapture(ctx, *producer.restore)
	if err != nil {
		return err
	}
	record, err := q.readBackings(capture)
	if err != nil {
		return err
	}
	images := nativeImageSourceJournal{owner: r.journal, backend: r.imageSources, helperGroups: r.helperGroups}
	if err := images.requireRetiredSnapshotBackings(record, v.chrootRoot(capture.InstanceID)); err != nil {
		return err
	}
	directory, err := nativeDiskImageRootIdentity(v.nativeImageStagingRoot)
	if err != nil || nativePublicationRootsOverlap(v.chrootBase, v.nativeImageStagingRoot) {
		return errors.Join(err, errors.New("native qualification restore: backing materialization requires original private disk"))
	}
	fd, err := unix.Open(v.nativeImageStagingRoot, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	root := os.NewFile(uintptr(fd), "native-restore-backing-root")
	defer func() { result = errors.Join(result, root.Close()) }()
	var files [2]*os.File
	defer func() {
		for _, file := range files {
			if file != nil {
				result = errors.Join(result, file.Close())
			}
		}
	}()
	for i, image := range record.Images {
		if err := errors.Join(v.requireNativeRestoreStagingOwner(owner), requireNativeRestoreInputRoot(v.nativeImageStagingRoot, root, directory)); err != nil {
			return err
		}
		files[i], err = materializeNativeRestoreBacking(ctx, root, paths[i], image)
		if err != nil {
			return err
		}
	}
	for i, image := range record.Images {
		if err := errors.Join(v.requireNativeRestoreStagingOwner(owner), requireNativeRestoreInputRoot(v.nativeImageStagingRoot, root, directory), ctx.Err()); err != nil {
			return err
		}
		_, err := images.stagePrepared(ctx, owner, v.chrootRoot(owner.Lease.Instance), image.Name, true, 0o044,
			func(original nativeLaunchRecord) (nativeImagePreparation, error) {
				return backend.PrepareRestoreBacking(ctx, original, v.chrootRoot(original.Lease.Instance), files[i], image)
			})
		if err != nil {
			return err
		}
	}
	return errors.Join(v.requireNativeRestoreStagingOwner(owner), images.requireRetiredSnapshotBackings(record, v.chrootRoot(capture.InstanceID)), images.require(ctx, owner, false), ctx.Err())
}

func (j *nativeImageSourceJournal) requireRetiredSnapshotBackings(record nativeSnapshotBackingRecord, root string) error {
	physical, err := j.owner.read(record.Capture.InstanceID)
	if err != nil || physical.Generation != record.Capture.NativeGeneration || physical.KernelBootID != record.Capture.KernelBootID ||
		!physical.Revoked || !physical.ExitConfirmed || !physical.ResourcesRemoved {
		return errors.Join(err, errors.New("native snapshot backing: original physical retirement is unavailable"))
	}
	sources, err := j.records()
	if err != nil {
		return err
	}
	for _, image := range record.Images {
		found := false
		for _, source := range sources {
			if source.Epoch != image.Epoch || source.Identity != image.Identity {
				continue
			}
			for _, ref := range source.References {
				if ref.ID == image.ReferenceID && ref.Name == image.Name && ref.Root == root && sameNativeImageOwner(ref, physical) &&
					ref.ReadOnly && ref.Ready && ref.Removed && ref.TargetRemoved && source.Ready {
					found = true
				}
			}
		}
		if !found {
			return errors.New("native snapshot backing: captured source reference is absent or changed")
		}
	}
	return nil
}

func materializeNativeRestoreBacking(ctx context.Context, root *os.File, path string, image nativeSnapshotBackingImage) (input *os.File, result error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || image.LogicalBytes <= 0 || image.LogicalBytes == math.MaxInt64 {
		return nil, errors.New("native snapshot backing: bounded original image and absolute content candidate are required")
	}
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	source := os.NewFile(uintptr(fd), "native-unverified-backing-candidate")
	defer func() {
		result = errors.Join(result, source.Close())
		if result != nil && input != nil {
			result = errors.Join(result, input.Close())
			input = nil
		}
	}()
	info, err := source.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() != image.LogicalBytes {
		return nil, errors.Join(err, errors.New("native snapshot backing: content candidate differs from original size"))
	}
	fd, err = unix.Openat(int(root.Fd()), ".", unix.O_TMPFILE|unix.O_RDWR|unix.O_CLOEXEC, 0o600)
	if err != nil {
		return nil, err
	}
	output := os.NewFile(uintptr(fd), "native-unverified-backing-copy")
	defer func() {
		result = errors.Join(result, output.Close())
	}()
	n, err := io.CopyBuffer(output, nativeImageCopyReader{ctx: ctx, source: io.NewSectionReader(source, 0, image.LogicalBytes+1)}, make([]byte, 128*1024))
	if err != nil || n != image.LogicalBytes {
		return nil, errors.Join(err, errors.New("native snapshot backing: original bounded content copy is incomplete"))
	}
	if err := errors.Join(verifyNativeRestoreDigest(ctx, output, image.LogicalBytes, image.SHA256), output.Chmod(0o400), output.Sync(), ctx.Err()); err != nil {
		return nil, err
	}
	fd, err = unix.Open(nativeImageFDPath(output), unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	input = os.NewFile(uintptr(fd), "native-verified-backing-input")
	a, _, statErr := nativeImageFileMetadata(output)
	b, _, readErr := nativeImageFileMetadata(input)
	if err := errors.Join(statErr, readErr, requireNativeSealedRestoreDescriptor(input, image.LogicalBytes)); err != nil || a != b {
		return input, errors.Join(err, errors.New("native snapshot backing: sealed copy lost original anonymous identity"))
	}
	return input, ctx.Err()
}

func (b linuxNativeImageSources) PrepareRestoreBacking(ctx context.Context, owner nativeLaunchRecord, root string, input *os.File, image nativeSnapshotBackingImage) (nativeImagePreparation, error) {
	if err := image.validate(); err != nil {
		return nil, err
	}
	prepared, err := b.prepareVerifiedRestoreImage(ctx, owner, root, input, image.Name, image.LogicalBytes, image.SHA256)
	if err == nil {
		prepared.(*linuxNativeImagePreparation).restoreBacking = &image
	}
	return prepared, err
}
