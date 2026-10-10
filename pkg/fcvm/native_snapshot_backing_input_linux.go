//go:build linux

// adr: 568 — shared backing reads keep the original native inode pinned.
package fcvm

import (
	"context"
	"errors"
	"os"
	"path/filepath"
)

func (b linuxNativeImageSources) OpenSnapshotBacking(record nativeImageSourceRecord, ref nativeImageReference, point string) (file *os.File, result error) {
	if err := record.validate(ref.Owner.KernelBootID); err != nil {
		return nil, err
	}
	retained := false
	for _, original := range record.References {
		retained = retained || original == ref
	}
	anchor := filepath.Join(b.base, ".native-processes", "image-sources", "points", record.Epoch)
	if !retained || point != anchor || record.Removed || !record.Ready || record.Applied != record.Desired || ref.Removed || !ref.Ready || ref.TargetRemoved ||
		!ref.ReadOnly || !nativeSnapshotBackingName(ref.Name) || filepath.Dir(filepath.Dir(filepath.Dir(ref.Root))) != b.base || !nativeExecutableName(filepath.Base(filepath.Dir(filepath.Dir(ref.Root))), "firecracker") {
		return nil, errors.New("native snapshot backing: original read-only binding is required")
	}
	if err := b.CheckReference(record, ref); err != nil {
		return nil, err
	}
	file, err := openNativeImageAnchor(record, point)
	if err != nil {
		return nil, err
	}
	_, metadata, statErr := nativeImageFileMetadata(file)
	if err := errors.Join(statErr, b.CheckAnchor(record, point)); err != nil || metadata != record.Applied {
		return nil, errors.Join(err, file.Close(), errors.New("native snapshot backing: original source metadata changed"))
	}
	return file, nil
}

func (v *JailerVMM) captureNativeSnapshotBackings(ctx context.Context, lease Lease, backing BackingIdentity) (result error) {
	permit, _, err := nativeSnapshotPublicationKeys(ctx, lease)
	if err != nil {
		return err
	}
	r := v.nativeRecovery
	if r == nil || r.journal == nil || r.generation(lease.Instance) != permit.Incoming.NativeGeneration {
		return errors.New("native snapshot backing: original native producer is unavailable")
	}
	if err := r.checkDaemonOwnership(); err != nil {
		return err
	}
	lock, err := r.journal.lock(ctx, lease.Instance)
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, lock.Close(), ctx.Err()) }()
	owner, err := r.journal.read(lease.Instance)
	if err != nil {
		return err
	}
	images := nativeImageSourceJournal{owner: r.journal, backend: r.imageSources}
	if err := images.captureOutputAuthority(ctx, permit.Physical, owner, permit); err != nil {
		return err
	}
	bindings, err := images.snapshotBackings(owner, v.chrootRoot(lease.Instance))
	if err != nil {
		return err
	}
	record := nativeSnapshotBackingRecord{Version: 1, Capture: permit.Capture, Backing: backing}
	for _, binding := range bindings {
		image, err := images.captureBackingLocked(ctx, owner, v.chrootRoot(lease.Instance), binding)
		if err != nil {
			return err
		}
		switch "sha256:" + image.SHA256 {
		case backing.Kernel:
			record.Images[0] = image
		case backing.Base:
			record.Images[1] = image
		default:
			return ErrSnapshotBackingUnverified
		}
	}
	if err := errors.Join(images.captureOutputAuthority(ctx, permit.Physical, owner, permit), r.checkDaemonOwnership()); err != nil {
		return err
	}
	return r.journal.qualifications(permit.Incoming.Execution.NodeID).writeBackings(record)
}
