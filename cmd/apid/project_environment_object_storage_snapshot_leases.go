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
	lease          state.ProjectEnvironmentCloneLease
	provider       objectstorage.Provider
	sourcePhysical string
}

func (s cloneObjectSnapshotLeaseAdapter) verifyRetention(ctx context.Context, manifest state.ProjectEnvironmentCloneObjectManifest) error {
	observer, ok := s.provider.(objectstorage.ObjectVersionRetentionObserver)
	if !ok {
		return objectstorage.ErrObjectSnapshotRetentionUnavailable
	}
	all := make([]state.ProjectEnvironmentCloneObjectVersion, len(manifest.Objects))
	remaining := make([]objectstorage.ObjectVersion, 0, len(manifest.Objects))
	for i, checkpoint := range manifest.Objects {
		item := checkpoint.Source
		all[i] = item
		if checkpoint.CopiedAt == nil {
			remaining = append(remaining, objectstorage.ObjectVersion{Key: item.Key, VersionID: item.VersionID, MetadataVersion: item.MetadataVersion,
				Size: item.Size, ETag: item.ETag, LastModified: item.LastModified, ValidUntil: item.ValidUntil, Deleted: item.Deleted})
		}
	}
	hash, err := state.ProjectEnvironmentCloneObjectManifestHash(all)
	if err != nil || hash != manifest.Hash {
		return state.ErrConflict
	}
	return objectstorage.VerifyObjectManifestRetention(ctx, observer, s.sourcePhysical, remaining, s.lease.ExpiresAt)
}

func (s cloneObjectSnapshotLeaseAdapter) ProjectEnvironmentCloneObjectManifest(ctx context.Context, accountID, projectID, operationID, sourceBucketID string) (state.ProjectEnvironmentCloneObjectManifest, error) {
	op := s.lease.Operation
	if accountID != op.AccountID || projectID != op.ProjectID || operationID != op.ID {
		return state.ProjectEnvironmentCloneObjectManifest{}, state.ErrNotFound
	}
	manifest, err := s.ProjectEnvironmentCloneLeasedObjectManifestStore.ProjectEnvironmentCloneObjectManifest(ctx, accountID, projectID, operationID, sourceBucketID)
	if err == nil {
		err = s.verifyRetention(ctx, manifest)
	}
	return manifest, err
}

func (s cloneObjectSnapshotLeaseAdapter) PutProjectEnvironmentCloneObjectManifest(ctx context.Context, accountID, projectID string, manifest state.ProjectEnvironmentCloneObjectManifest) (state.ProjectEnvironmentCloneObjectManifest, error) {
	op := s.lease.Operation
	if accountID != op.AccountID || projectID != op.ProjectID || manifest.OperationID != op.ID {
		return state.ProjectEnvironmentCloneObjectManifest{}, state.ErrNotFound
	}
	if err := s.verifyRetention(ctx, manifest); err != nil {
		return state.ProjectEnvironmentCloneObjectManifest{}, err
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
	adapter := cloneObjectSnapshotLeaseAdapter{ProjectEnvironmentCloneLeasedObjectManifestStore: store, lease: lease, provider: provider, sourcePhysical: sourcePhysical}
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
	adapter := cloneObjectSnapshotLeaseAdapter{ProjectEnvironmentCloneLeasedObjectManifestStore: store, lease: lease, provider: provider, sourcePhysical: sourcePhysical}
	return copyProjectEnvironmentObjectStorageSnapshot(callCtx, adapter, provider, op.AccountID, op.ProjectID, op.ID,
		sourceBucketID, targetBucketID, sourcePhysical, targetPhysical)
}
