// adr: 568 — publication consumes only original capture-owned descriptors.
package fcvm

import (
	"context"
	"errors"
	"os"

	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

// This preflight grants no pause, output completion or publication receipt. The
// complete capture producer must use it before its first Firecracker effect.
func (v *JailerVMM) preflightNativeSnapshotPublication(ctx context.Context, lease Lease) (keys state.EnvironmentQualificationSnapshot, err error) {
	backend := v.storage
	if p, ok := ctx.Value(nativeSnapshotPublicationContextKey{}).(nativeSnapshotPublicationPermit); ok {
		backend = p.backend
	}
	return v.preflightNativeSnapshotPublicationTo(ctx, lease, backend)
}

func (v *JailerVMM) preflightNativeSnapshotPublicationTo(ctx context.Context, lease Lease, backend storage.StorageBackend) (keys state.EnvironmentQualificationSnapshot, err error) {
	permit, keys, err := nativeSnapshotPublicationKeys(ctx, lease)
	if err != nil {
		return keys, err
	}
	r := v.nativeRecovery
	if r == nil || r.journal == nil || r.generation(lease.Instance) != permit.Incoming.NativeGeneration {
		return keys, errors.New("native snapshot publication: original local producer is required")
	}
	if err := r.checkDaemonOwnership(); err != nil {
		return keys, err
	}
	lock, err := r.journal.lock(ctx, lease.Instance)
	if err != nil {
		return keys, err
	}
	defer func() { err = errors.Join(err, lock.Close()) }()
	owner, err := r.journal.read(lease.Instance)
	if err != nil {
		return keys, err
	}
	images := nativeImageSourceJournal{owner: r.journal, backend: r.imageSources}
	if err := images.captureOutputAuthority(ctx, permit.Physical, owner, permit); err != nil {
		return keys, err
	}
	for _, key := range []string{keys.StorageKey, keys.VMStateStorageKey, keys.DriveStorageKey, keys.BackingStorageKey} {
		if err := storage.CheckExclusivePut(ctx, backend, key); err != nil {
			return keys, err
		}
	}
	return keys, nil
}

func nativeSnapshotPublicationKeys(ctx context.Context, lease Lease) (permit nativeSnapshotCapturePermit, keys state.EnvironmentQualificationSnapshot, err error) {
	permit, ok := ctx.Value(nativeSnapshotCaptureContextKey{}).(nativeSnapshotCapturePermit)
	if !ok || permit.Incoming.Execution.InstanceID != lease.Instance || !sameNativePhysicalLease(lease, permit.Incoming.NativeLease) ||
		!liveNativeSnapshotOwner(permit.Physical) || !sameNativePhysicalLease(lease, permit.Physical.Lease) {
		return permit, keys, errors.New("native snapshot publication: original capture capability is required")
	}
	if err := permit.Incoming.validate(permit.Incoming.Execution.NodeID); err != nil {
		return permit, keys, err
	}
	if err := permit.Capture.validate(permit.Incoming); err != nil {
		return permit, keys, err
	}
	if !permit.Capture.CompletedAt.IsZero() || !permit.Incoming.CreateStarted || permit.Incoming.Revoked {
		return permit, keys, errors.New("native snapshot publication: original incomplete capture is required")
	}
	return permit, qualificationSnapshotProof(permit.Incoming, SnapshotInfo{}), ctx.Err()
}

// The reader boundary owns the physical and original output locks until the
// exclusive writer has joined all IO and the descriptor has closed. Repeated or
// uncertain publication cannot overwrite an existing object. This helper does
// not prove that pause/create/freeze/resume or the other three objects completed.
// Persistent begin intent is required and never reconstructed from inventory.
func (v *JailerVMM) publishNativeSnapshotOutput(ctx context.Context, lease Lease, kind string) error {
	if kind != "mem" && kind != "vmstate" {
		return errors.New("native snapshot publication: unsupported original output kind")
	}
	publication, err := v.requireNativeSnapshotPublication(ctx, lease)
	if err != nil {
		return err
	}
	permit, _, err := nativeSnapshotPublicationKeys(ctx, lease)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithDeadline(ctx, permit.Incoming.Deadline)
	defer cancel()
	keys, err := v.preflightNativeSnapshotPublication(ctx, lease)
	if err != nil {
		return err
	}
	key := keys.StorageKey
	if kind == "vmstate" {
		key = keys.VMStateStorageKey
	}
	return v.withNativeSnapshotOutput(ctx, lease, kind, func(file *os.File) error {
		info, err := file.Stat()
		if err != nil {
			return err
		}
		if err := storage.PutExclusive(ctx, publication.backend, key, file, info.Size()); err != nil {
			return err
		}
		r := v.nativeRecovery
		if r.generation(lease.Instance) != permit.Incoming.NativeGeneration {
			return errors.New("native snapshot publication: original daemon producer changed during IO")
		}
		if err := r.checkDaemonOwnership(); err != nil {
			return err
		}
		current, err := r.journal.read(lease.Instance)
		if err != nil {
			return err
		}
		images := nativeImageSourceJournal{owner: r.journal, backend: r.imageSources}
		if err := images.captureOutputAuthority(ctx, permit.Physical, current, permit); err != nil {
			return err
		}
		_, err = v.requireNativeSnapshotPublication(ctx, lease)
		return err
	})
}
