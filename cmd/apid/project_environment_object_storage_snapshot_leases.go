package main

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

type cloneObjectSnapshotWorkerStore interface {
	state.ProjectEnvironmentCloneLeasedObjectManifestStore
	state.ProjectEnvironmentCloneWorkerLeaseStore
}

// The snapshot helpers share their byte-verification path with the legacy
// prototypes. Every worker mutation uses its explicit, non-public authority.
type cloneObjectSnapshotLeaseAdapter struct {
	state.ProjectEnvironmentCloneLeasedObjectManifestStore
	lease state.ProjectEnvironmentCloneLease
}

func (s cloneObjectSnapshotLeaseAdapter) PutProjectEnvironmentCloneObjectManifest(ctx context.Context, accountID, projectID string, manifest state.ProjectEnvironmentCloneObjectManifest) (state.ProjectEnvironmentCloneObjectManifest, error) {
	op := s.lease.Operation
	if accountID != op.AccountID || projectID != op.ProjectID || manifest.OperationID != op.ID {
		return state.ProjectEnvironmentCloneObjectManifest{}, state.ErrNotFound
	}
	return s.PutProjectEnvironmentCloneObjectManifestForLease(ctx, s.lease, manifest)
}

func (s cloneObjectSnapshotLeaseAdapter) MarkProjectEnvironmentCloneObjectCopied(ctx context.Context, accountID, projectID, operationID, sourceBucketID, key, sourceVersion, targetETag, verifiedSHA256 string) error {
	op := s.lease.Operation
	if accountID != op.AccountID || projectID != op.ProjectID || operationID != op.ID {
		return state.ErrNotFound
	}
	return s.MarkProjectEnvironmentCloneObjectCopiedForLease(ctx, s.lease, sourceBucketID, key, sourceVersion, targetETag, verifiedSHA256)
}

func captureProjectEnvironmentObjectStorageSnapshotForLease(ctx context.Context, store cloneObjectSnapshotWorkerStore, provider objectstorage.Provider, lease state.ProjectEnvironmentCloneLease,
	sourceBucketID, targetBucketID, sourcePhysical string, point time.Time) (state.ProjectEnvironmentCloneObjectManifest, error) {
	lease, err := store.RenewProjectEnvironmentCloneLease(ctx, lease, projectEnvironmentCloneWorkerLeaseDuration)
	if err != nil {
		return state.ProjectEnvironmentCloneObjectManifest{}, err
	}
	if lease.Operation.Status != state.CloneOperationCapturing {
		return state.ProjectEnvironmentCloneObjectManifest{}, state.ErrConflict
	}
	callCtx, cancel := context.WithDeadline(ctx, lease.ExpiresAt)
	defer cancel()
	op := lease.Operation
	adapter := cloneObjectSnapshotLeaseAdapter{ProjectEnvironmentCloneLeasedObjectManifestStore: store, lease: lease}
	return captureProjectEnvironmentObjectStorageSnapshot(callCtx, adapter, provider, op.AccountID, op.ProjectID, op.ID,
		sourceBucketID, targetBucketID, sourcePhysical, point)
}

func copyProjectEnvironmentObjectStorageSnapshotForLease(ctx context.Context, store cloneObjectSnapshotWorkerStore, provider objectstorage.Provider, lease state.ProjectEnvironmentCloneLease,
	sourceBucketID, targetBucketID, sourcePhysical, targetPhysical string) (int, error) {
	lease, err := store.RenewProjectEnvironmentCloneLease(ctx, lease, projectEnvironmentCloneWorkerLeaseDuration)
	if err != nil {
		return 0, err
	}
	if lease.Operation.Status != state.CloneOperationCopying {
		return 0, state.ErrConflict
	}
	callCtx, cancel := context.WithDeadline(ctx, lease.ExpiresAt)
	defer cancel()
	op := lease.Operation
	adapter := cloneObjectSnapshotLeaseAdapter{ProjectEnvironmentCloneLeasedObjectManifestStore: store, lease: lease}
	return copyProjectEnvironmentObjectStorageSnapshot(callCtx, adapter, provider, op.AccountID, op.ProjectID, op.ID,
		sourceBucketID, targetBucketID, sourcePhysical, targetPhysical)
}
