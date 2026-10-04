// adr: 521 — native export inputs retain original inode and physical ownership.
package fcvm

import (
	"context"
	"errors"
	"os"
	"path/filepath"
)

// This is an input capability only. It supplies neither output producer
// ownership nor pause, publication, restore or qualification evidence.
type nativeSnapshotInputBackend interface {
	OpenSnapshotInput(nativeImageSourceRecord, nativeImageReference, string) (*os.File, error)
}

// Recovered journal entries never supply local producer authority. A future
// export adapter must enter through this boundary while its Manager flight is
// pinned; the source capability alone cannot start a snapshot.
func (v *JailerVMM) withNativeSnapshotDriveInput(ctx context.Context, lease Lease, consume func(*os.File) error) error {
	r := v.nativeRecovery
	if r == nil || r.journal == nil {
		return errors.New("native snapshot input: native owner is unavailable")
	}
	r.mu.Lock()
	generation, daemonLock := r.owned[lease.Instance], r.daemonLock
	r.mu.Unlock()
	if generation == "" || daemonLock == nil {
		return errors.New("native snapshot input: original local producer authority is required")
	}
	if _, err := daemonLock.Stat(); err != nil {
		return err
	}
	owner, err := r.journal.read(lease.Instance)
	if err != nil {
		return err
	}
	if owner.Generation != generation || !sameNativePhysicalLease(owner.Lease, lease) {
		return errors.New("native snapshot input: local producer differs from original physical owner")
	}
	j := nativeImageSourceJournal{owner: r.journal, backend: r.imageSources}
	return j.withSnapshotDrive(ctx, owner, v.chrootRoot(lease.Instance), consume)
}

// withSnapshotDrive keeps the original VM and source epoch locked until the
// consumer has returned and its descriptor is closed. A consumer must finish
// all reads synchronously; it cannot retain the descriptor or its procfs path.
// Lock order remains incoming qualification (when present), VM, then source.
func (j *nativeImageSourceJournal) withSnapshotDrive(ctx context.Context, expected nativeLaunchRecord, root string, consume func(*os.File) error) (err error) {
	backend, ok := j.backend.(nativeSnapshotInputBackend)
	if !ok || consume == nil {
		return errors.New("native snapshot input: pinned source backend or consumer is unavailable")
	}
	if err := expected.validate(expected.Lease.Instance); err != nil {
		return err
	}
	if !liveNativeSnapshotOwner(expected) || !filepath.IsAbs(root) || filepath.Clean(root) != root || filepath.Base(root) != "root" || filepath.Base(filepath.Dir(root)) != expected.Lease.Instance {
		return errors.New("native snapshot input: original live VM authority is required")
	}
	vmLock, err := j.owner.lock(ctx, expected.Lease.Instance)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, vmLock.Close()) }()
	current, err := j.owner.read(expected.Lease.Instance)
	if err != nil {
		return err
	}
	if !liveNativeSnapshotOwner(current) || current.Generation != expected.Generation || current.KernelBootID != expected.KernelBootID || current.PID != expected.PID || current.StartTime != expected.StartTime || !sameNativePhysicalLease(current.Lease, expected.Lease) {
		return errors.New("native snapshot input: original physical owner changed")
	}
	record, ref, err := j.snapshotDrive(current, root)
	if err != nil {
		return err
	}
	sourceLock, err := j.lock(ctx, record.Identity)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, sourceLock.Close()) }()
	// Discovery was outside the source lock. Never substitute a later epoch,
	// source inode, namespace or reference after waiting for the original lock.
	lockedRecord, lockedRef, err := j.snapshotDrive(current, root)
	if err != nil {
		return err
	}
	if lockedRecord.Epoch != record.Epoch || lockedRecord.Identity != record.Identity || lockedRecord.Namespace != record.Namespace || lockedRef != ref {
		return errors.New("native snapshot input: original source epoch changed")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	file, err := backend.OpenSnapshotInput(lockedRecord, lockedRef, j.anchor(lockedRecord))
	if err != nil {
		// A backend returning an error still transfers any returned descriptor.
		if file != nil {
			err = errors.Join(err, file.Close())
		}
		return err
	}
	if file == nil {
		return errors.New("native snapshot input: backend returned no pinned descriptor")
	}
	// Registered last: the descriptor closes before either ownership lock.
	defer func() { err = errors.Join(err, file.Close()) }()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	identity, err := resourceFileID(info)
	if err != nil || identity.Device != lockedRecord.Identity.Device || identity.Inode != lockedRecord.Identity.Inode {
		return errors.Join(err, errors.New("native snapshot input: pinned descriptor differs from original source"))
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return errors.Join(consume(file), ctx.Err())
}

func liveNativeSnapshotOwner(owner nativeLaunchRecord) bool {
	return owner.Authorized && owner.PID > 0 && owner.StartTime != 0 && !owner.Revoked && !owner.ExitConfirmed && !owner.ResourcesRemoved && !owner.Lease.IsBuilder
}

func (j *nativeImageSourceJournal) snapshotDrive(owner nativeLaunchRecord, root string) (record nativeImageSourceRecord, ref nativeImageReference, err error) {
	records, err := j.records()
	if err != nil {
		return record, ref, err
	}
	found := false
	for _, candidate := range records {
		for _, reference := range candidate.References {
			if !sameNativeImageOwner(reference, owner) || reference.Root != root || reference.Name != layerImageName || reference.Removed {
				continue
			}
			if found {
				return record, ref, errors.New("native snapshot input: private drive has ambiguous source ownership")
			}
			if candidate.Removed || !candidate.Ready || candidate.Applied != candidate.Desired || !reference.Ready || reference.TargetRemoved || reference.ReadOnly || reference.Link {
				return record, ref, errors.New("native snapshot input: private drive lacks complete writable binding proof")
			}
			record, ref, found = candidate, reference, true
		}
	}
	if !found {
		return record, ref, errors.New("native snapshot input: original private drive is absent")
	}
	return record, ref, nil
}
