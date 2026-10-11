//go:build linux

// adr: 568 — the original native producer owns the complete capture interval.
package fcvm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"

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
	if err := v.captureNativeSnapshotBackings(ctx, lease, backing); err != nil {
		return info, err
	}
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
	if err := r.imageSources.(nativeSnapshotOutputHandoffBackend).HandoffSnapshotOutputs(ctx, v, owner); err != nil {
		return info, err
	}
	owner, err = v.checkNativeSnapshotPublicationOwner(ctx, lease)
	if err != nil {
		return info, err
	}
	// All bindings must be complete before even the first pause request.
	if err := images.requireSnapshotControlBindings(owner, v.chrootRoot(lease.Instance), permit.Capture.CaptureID, nativeSnapshotCreate); err != nil {
		return info, err
	}
	allowance, err := r.snapshotMemory.Prepare(ctx, v, owner)
	if err != nil {
		return info, fmt.Errorf("native snapshot capture: prepare memory fence: %w", err)
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), v.nativeCleanupBudget())
		defer stop()
		result = errors.Join(result, allowance.Restore(cleanup), allowance.Close())
	}()
	if _, err := v.checkNativeSnapshotPublicationOwner(ctx, lease); err != nil {
		return info, err
	}
	if err := v.controlNativeQualificationSnapshotLocked(ctx, lease, permit, owner, nativeSnapshotPause); err != nil {
		return info, fmt.Errorf("native snapshot capture: pause original VM: %w", err)
	}
	if _, err := v.checkNativeSnapshotPublicationOwner(ctx, lease); err != nil {
		return info, err
	}
	if err := allowance.Raise(ctx); err != nil {
		return info, fmt.Errorf("native snapshot capture: raise memory fence: %w", err)
	}
	if _, err := v.checkNativeSnapshotPublicationOwner(ctx, lease); err != nil {
		return info, err
	}
	if err := v.controlNativeQualificationSnapshotLocked(ctx, lease, permit, owner, nativeSnapshotCreate); err != nil {
		return info, fmt.Errorf("native snapshot capture: create original snapshot: %w", err)
	}
	if _, err := v.checkNativeSnapshotPublicationOwner(ctx, lease); err != nil {
		return info, err
	}
	frozen := r.imageSources.(nativeSnapshotFrozenDriveBackend) // preflight required this exact runtime adapter
	err = images.withSnapshotDriveLocked(ctx, owner, v.chrootRoot(lease.Instance), func(input *os.File) error {
		return withNativeFrozenDrive(ctx, frozen, input, v.nativeImageStagingRoot, func(drive *os.File) error {
			if _, err := v.checkNativeSnapshotPublicationOwner(ctx, lease); err != nil {
				return err
			}
			if err := allowance.Restore(ctx); err != nil {
				return fmt.Errorf("native snapshot capture: restore memory fence: %w", err)
			}
			if _, err := v.checkNativeSnapshotPublicationOwner(ctx, lease); err != nil {
				return err
			}
			if err := allowance.RequireRestored(ctx); err != nil {
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
			if err := allowance.RequireRestored(ctx); err != nil {
				return err
			}
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
	if r == nil || r.journal == nil || r.publications == nil || r.snapshotControl == nil || r.snapshotMemory == nil || r.generation(lease.Instance) != permit.Incoming.NativeGeneration {
		return permit, errors.New("native snapshot capture: original native producer is unavailable")
	}
	_, input := r.imageSources.(nativeSnapshotInputBackend)
	_, backingInput := r.imageSources.(nativeSnapshotBackingInputBackend)
	_, output := r.imageSources.(nativeSnapshotOutputBackend)
	_, reader := r.imageSources.(nativeSnapshotOutputInputBackend)
	_, frozen := r.imageSources.(nativeSnapshotFrozenDriveBackend)
	_, handoff := r.imageSources.(nativeSnapshotOutputHandoffBackend)
	disk, preflight := r.imageSources.(nativeSnapshotCapturePreflightBackend)
	if !input || !backingInput || !output || !reader || !frozen || !preflight || !handoff {
		return permit, errors.New("native snapshot capture: complete native source adapters are required")
	}
	if err := r.imageSources.(nativeSnapshotOutputHandoffBackend).CheckSnapshotOutputHandoff(ctx, v); err != nil {
		return permit, err
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
	if err := r.snapshotMemory.Check(ctx, v, owner); err != nil {
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
	journal, ok := publication.journal.(nativeSnapshotPublicationReceiptJournal)
	if !ok {
		return info, errors.New("native snapshot capture: original receipt journal is unavailable")
	}
	var receipts []nativeSnapshotPublicationObjectReceipt
	for _, kind := range []string{"mem", "vmstate"} {
		if err := images.withCaptureOutputLocked(ctx, permit, v.chrootRoot(lease.Instance), kind, func(file *os.File) error {
			receipt, err := v.publishNativeSnapshotReader(ctx, lease, publication, nativePublicationObjectKey(publication.intent, kind), file)
			if err == nil {
				receipts = append(receipts, receipt)
			}
			return err
		}); err != nil {
			return SnapshotInfo{}, err
		}
	}
	driveReceipt, err := v.publishNativeSnapshotReader(ctx, lease, publication, publication.intent.Keys.Drive, drive)
	if err != nil {
		return SnapshotInfo{}, err
	}
	receipts = append(receipts, driveReceipt)
	body, err := json.Marshal(backing)
	if err != nil {
		return SnapshotInfo{}, err
	}
	backingReceipt, err := v.publishNativeSnapshotArtifact(ctx, lease, publication, publication.intent.Keys.Backing, bytes.NewReader(body), int64(len(body)))
	if err != nil {
		return SnapshotInfo{}, err
	}
	receipts = append(receipts, backingReceipt)
	for _, receipt := range receipts {
		if err := journal.RequireObject(ctx, publication.intent, receipt); err != nil {
			return SnapshotInfo{}, err
		}
		if receipt.Kind != "backing" {
			if info.StoredBytes > math.MaxInt64-receipt.Object.StoredBytes {
				return SnapshotInfo{}, errors.New("native snapshot capture: stored artifact sizes overflow")
			}
			info.StoredBytes += receipt.Object.StoredBytes
		}
	}
	info.MemBytes, info.VMStateBytes = receipts[0].Object.LogicalBytes, receipts[1].Object.LogicalBytes
	// Stored accounting follows original encoded/allocated writer observations.
	// The backing sidecar is retained but excluded by SnapshotInfo's contract.
	return info, ctx.Err()
}
