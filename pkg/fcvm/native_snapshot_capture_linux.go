//go:build linux

// adr: 568 — the original native producer owns the complete capture interval.
package fcvm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"

	"github.com/onebox-faas/faas/pkg/storage"
	"golang.org/x/sys/unix"
)

type nativeSnapshotCapturePreflightBackend interface {
	CheckSnapshotCaptureDirectory(string) error
	CheckSnapshotCaptureRoot(context.Context, nativeLaunchRecord, string) error
}

func (b linuxNativeImageSources) CheckSnapshotCaptureRoot(ctx context.Context, owner nativeLaunchRecord, root string) error {
	var directory unix.Stat_t
	if err := unix.Lstat(root, &directory); err != nil {
		return err
	}
	if directory.Mode&unix.S_IFMT != unix.S_IFDIR || directory.Uid != uint32(owner.Lease.UID) || directory.Gid != uint32(owner.Lease.GID) {
		return errors.New("native snapshot capture: original live jail must belong to its VM UID")
	}
	permit, _, err := nativeSnapshotPublicationKeys(ctx, owner.Lease)
	if err != nil {
		return err
	}
	name, err := nativeSnapshotOutputName(permit.Capture.CaptureID, "mem")
	if err != nil {
		return err
	}
	prepared := owner
	prepared.Authorized, prepared.PID, prepared.StartTime = false, 0, 0
	file, err := b.prepareCaptureOutputRoot(ctx, prepared, root, name)
	if err != nil {
		return err
	}
	return file.Close()
}

func (b linuxNativeImageSources) CheckSnapshotCaptureDirectory(directory string) error {
	if directory == "" || directory != b.diskStagingRoot {
		return errors.New("native snapshot capture: original persistent disk staging root is required")
	}
	if _, err := nativeDiskImageRootIdentity(directory); err != nil {
		return err
	}
	return b.requireAnonymousStagingFilesystem(directory)
}

// This internal adapter is deliberately behind the closed qualification support
// gate. It owns all effects; the Manager must never add legacy resume, Put or
// failed-capture Delete around it. Incomplete/uncertain captures require the
// existing revoke/stop owner and a new qualification attempt, never effect replay.
func (v *JailerVMM) captureEnvironmentQualificationSnapshot(ctx context.Context, lease Lease, backing BackingIdentity) (info SnapshotInfo, result error) {
	defer func() {
		if result != nil {
			info = SnapshotInfo{}
		}
	}()
	permit, err := v.preflightNativeSnapshotCapture(ctx, lease, backing)
	if err != nil {
		return info, err
	}
	ctx, cancel := context.WithDeadline(ctx, permit.Incoming.Deadline)
	defer cancel()
	ctx, err = v.beginNativeSnapshotPublication(ctx, lease)
	if err != nil {
		return info, err
	}
	for _, kind := range []string{"mem", "vmstate"} {
		if _, err := v.requireNativeSnapshotPublication(ctx, lease); err != nil {
			return info, err
		}
		if _, err := v.stageNativeSnapshotOutput(ctx, lease, v.nativeImageStagingRoot, kind); err != nil {
			return info, err
		}
	}
	r := v.nativeRecovery
	lock, err := r.journal.lock(ctx, lease.Instance)
	if err != nil {
		return info, err
	}
	defer func() { result = errors.Join(result, lock.Close(), ctx.Err()) }()
	owner, err := v.checkNativeSnapshotPublicationOwner(ctx, lease)
	if err != nil {
		return info, err
	}
	images := &nativeImageSourceJournal{owner: r.journal, backend: r.imageSources}
	// All bindings must be complete before even the first pause request.
	if err := images.requireSnapshotControlBindings(owner, v.chrootRoot(lease.Instance), permit.Capture.CaptureID, nativeSnapshotCreate); err != nil {
		return info, err
	}
	for _, action := range []nativeSnapshotControlAction{nativeSnapshotPause, nativeSnapshotCreate} {
		if err := v.controlNativeQualificationSnapshotLocked(ctx, lease, permit, owner, action); err != nil {
			return info, err
		}
		if _, err := v.checkNativeSnapshotPublicationOwner(ctx, lease); err != nil {
			return info, err
		}
	}
	frozen := r.imageSources.(nativeSnapshotFrozenDriveBackend) // preflight required this exact runtime adapter
	err = images.withSnapshotDriveLocked(ctx, owner, v.chrootRoot(lease.Instance), func(input *os.File) error {
		return withNativeFrozenDrive(ctx, frozen, input, v.nativeImageStagingRoot, func(drive *os.File) error {
			if _, err := v.checkNativeSnapshotPublicationOwner(ctx, lease); err != nil {
				return err
			}
			// Freeze has completed while paused. Resume is one-shot and must be
			// acknowledged before any potentially slow storage publication.
			if err := v.controlNativeQualificationSnapshotLocked(ctx, lease, permit, owner, nativeSnapshotResume); err != nil {
				return err
			}
			if _, err := v.checkNativeSnapshotPublicationOwner(ctx, lease); err != nil {
				return err
			}
			var err error
			info, err = v.publishNativeSnapshotCohort(ctx, lease, permit, images, drive, backing)
			return err
		})
	})
	if err != nil {
		return info, err // Source/frozen descriptors close before the physical lock.
	}
	_, err = v.checkNativeSnapshotPublicationOwner(ctx, lease)
	return info, err
}

// No producer effect occurs during preflight. Unsupported capability or disk
// placement refuses before persistent begin, output creation or API requests.
func (v *JailerVMM) preflightNativeSnapshotCapture(ctx context.Context, lease Lease, backing BackingIdentity) (permit nativeSnapshotCapturePermit, result error) {
	if !backing.complete() {
		return permit, ErrSnapshotBackingUnverified
	}
	permit, _, err := nativeSnapshotPublicationKeys(ctx, lease)
	if err != nil {
		return permit, err
	}
	ctx, cancel := context.WithDeadline(ctx, permit.Incoming.Deadline)
	defer cancel()
	r := v.nativeRecovery
	if r == nil || r.journal == nil || r.publications == nil || r.snapshotControl == nil || r.generation(lease.Instance) != permit.Incoming.NativeGeneration {
		return permit, errors.New("native snapshot capture: original native producer is unavailable")
	}
	_, input := r.imageSources.(nativeSnapshotInputBackend)
	_, output := r.imageSources.(nativeSnapshotOutputBackend)
	_, reader := r.imageSources.(nativeSnapshotOutputInputBackend)
	_, frozen := r.imageSources.(nativeSnapshotFrozenDriveBackend)
	disk, preflight := r.imageSources.(nativeSnapshotCapturePreflightBackend)
	if !input || !output || !reader || !frozen || !preflight {
		return permit, errors.New("native snapshot capture: complete native source adapters are required")
	}
	if err := disk.CheckSnapshotCaptureDirectory(v.nativeImageStagingRoot); err != nil {
		return permit, err
	}
	if err := r.checkDaemonOwnership(); err != nil {
		return permit, err
	}
	lock, err := r.journal.lock(ctx, lease.Instance)
	if err != nil {
		return permit, err
	}
	defer func() { result = errors.Join(result, lock.Close(), ctx.Err()) }()
	owner, err := r.journal.read(lease.Instance)
	if err != nil {
		return permit, err
	}
	images := nativeImageSourceJournal{owner: r.journal, backend: r.imageSources}
	if err := images.captureOutputAuthority(ctx, permit.Physical, owner, permit); err != nil {
		return permit, err
	}
	if err := disk.CheckSnapshotCaptureRoot(ctx, owner, v.chrootRoot(lease.Instance)); err != nil {
		return permit, err
	}
	return permit, images.requireSnapshotControlBindings(owner, v.chrootRoot(lease.Instance), permit.Capture.CaptureID, nativeSnapshotPause)
}

// Physical and original drive locks remain held by the producer. Each original
// output lock remains held until its own reader and storage IO have closed.
func (v *JailerVMM) publishNativeSnapshotCohort(ctx context.Context, lease Lease, permit nativeSnapshotCapturePermit, images *nativeImageSourceJournal, drive *os.File, backing BackingIdentity) (info SnapshotInfo, err error) {
	publication, err := v.requireNativeSnapshotPublication(ctx, lease)
	if err != nil {
		return info, err
	}
	for _, output := range []struct {
		kind string
		key  string
		size *int64
	}{{"mem", publication.intent.Keys.Memory, &info.MemBytes}, {"vmstate", publication.intent.Keys.VMState, &info.VMStateBytes}} {
		if err := images.withCaptureOutputLocked(ctx, permit, v.chrootRoot(lease.Instance), output.kind, func(file *os.File) error {
			var err error
			*output.size, err = v.publishNativeSnapshotReader(ctx, lease, publication, output.key, file)
			return err
		}); err != nil {
			return SnapshotInfo{}, err
		}
	}
	driveBytes, err := v.publishNativeSnapshotReader(ctx, lease, publication, publication.intent.Keys.Drive, drive)
	if err != nil {
		return SnapshotInfo{}, err
	}
	body, err := json.Marshal(backing)
	if err != nil {
		return SnapshotInfo{}, err
	}
	if _, err := v.checkNativeSnapshotPublicationOwner(ctx, lease); err != nil {
		return SnapshotInfo{}, err
	}
	if err := storage.PutExclusive(ctx, publication.backend, publication.intent.Keys.Backing, bytes.NewReader(body), int64(len(body))); err != nil {
		return SnapshotInfo{}, err
	}
	if _, err := v.checkNativeSnapshotPublicationOwner(ctx, lease); err != nil {
		return SnapshotInfo{}, err
	}
	if info.MemBytes > math.MaxInt64-info.VMStateBytes || info.MemBytes+info.VMStateBytes > math.MaxInt64-driveBytes {
		return SnapshotInfo{}, errors.New("native snapshot capture: logical artifact sizes overflow")
	}
	// The exclusive writer supplies no allocated-byte receipt yet. Logical
	// accounting is conservative and excludes the small backing sidecar,
	// matching SnapshotInfo's memory/state/private-drive contract.
	info.StoredBytes = info.MemBytes + info.VMStateBytes + driveBytes
	return info, ctx.Err()
}
