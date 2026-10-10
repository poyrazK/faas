// adr: 568 — publication consumes only original capture-owned descriptors.
package fcvm

import (
	"context"
	"errors"
	"io"
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
		if err := storage.CheckExclusiveArtifact(ctx, backend, key); err != nil {
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
		_, err := v.publishNativeSnapshotReader(ctx, lease, publication, key, file)
		return err
	})
}

// Caller holds the physical and source locks through IO and descriptor close.
// Original backend receipts are persisted before success. Neither a key intent
// nor a receipt independently grants retirement or qualification authority.
func (v *JailerVMM) publishNativeSnapshotReader(ctx context.Context, lease Lease, publication nativeSnapshotPublicationPermit, key string, file *os.File) (nativeSnapshotPublicationObjectReceipt, error) {
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 {
		return nativeSnapshotPublicationObjectReceipt{}, errors.Join(err, errors.New("native snapshot publication: original source is incomplete"))
	}
	return v.publishNativeSnapshotArtifact(ctx, lease, publication, key, file, info.Size())
}

func (v *JailerVMM) publishNativeSnapshotArtifact(ctx context.Context, lease Lease, publication nativeSnapshotPublicationPermit, key string, reader io.Reader, size int64) (nativeSnapshotPublicationObjectReceipt, error) {
	var empty nativeSnapshotPublicationObjectReceipt
	if _, err := v.checkNativeSnapshotPublicationOwner(ctx, lease); err != nil {
		return empty, err
	}
	journal, ok := publication.journal.(nativeSnapshotPublicationReceiptJournal)
	if !ok {
		return empty, errors.New("native snapshot publication: original receipt journal is required")
	}
	kind := ""
	for _, candidate := range []string{"mem", "vmstate", "drive", "backing"} {
		if nativePublicationObjectKey(publication.intent, candidate) == key {
			kind = candidate
		}
	}
	if kind == "" {
		return empty, errors.New("native snapshot publication: object is outside original intent")
	}
	object, err := storage.PutExclusiveArtifact(ctx, publication.backend, key, reader, size)
	if err != nil {
		return empty, err
	}
	if _, err := v.checkNativeSnapshotPublicationOwner(ctx, lease); err != nil {
		return empty, err
	}
	receipt, err := journal.RecordObject(ctx, publication.intent, kind, object)
	if err != nil {
		return empty, err
	}
	if _, err := v.checkNativeSnapshotPublicationOwner(ctx, lease); err != nil {
		return empty, err
	}
	if err := journal.RequireObject(ctx, publication.intent, receipt); err != nil {
		return empty, err
	}
	return receipt, ctx.Err()
}

// Caller holds the physical lock. Never take an incoming or physical lock
// recursively while checking the original permit and persistent intent.
func (v *JailerVMM) checkNativeSnapshotPublicationOwner(ctx context.Context, lease Lease) (nativeLaunchRecord, error) {
	var owner nativeLaunchRecord
	if _, err := v.requireNativeSnapshotPublication(ctx, lease); err != nil {
		return owner, err
	}
	permit, _, err := nativeSnapshotPublicationKeys(ctx, lease)
	if err != nil {
		return owner, err
	}
	r := v.nativeRecovery
	if r.generation(lease.Instance) != permit.Incoming.NativeGeneration {
		return owner, errors.New("native snapshot publication: original daemon producer changed during IO")
	}
	if err := r.checkDaemonOwnership(); err != nil {
		return owner, err
	}
	owner, err = r.journal.read(lease.Instance)
	if err != nil {
		return owner, err
	}
	images := nativeImageSourceJournal{owner: r.journal, backend: r.imageSources}
	return owner, images.captureOutputAuthority(ctx, permit.Physical, owner, permit)
}
