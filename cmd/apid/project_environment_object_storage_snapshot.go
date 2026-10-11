package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

// captureProjectEnvironmentObjectStorageSnapshot commits the exact source
// versions before copying. A retry adopts the committed manifest instead of
// listing a newer source state.
func captureProjectEnvironmentObjectStorageSnapshot(
	ctx context.Context,
	store state.ProjectEnvironmentCloneObjectManifestStore,
	provider objectstorage.Provider,
	accountID, projectID, operationID, sourceBucketID, targetBucketID, sourcePhysical string,
	capturedAt time.Time,
) (state.ProjectEnvironmentCloneObjectManifest, error) {
	if existing, err := store.ProjectEnvironmentCloneObjectManifest(ctx, accountID, projectID, operationID, sourceBucketID); err == nil {
		if existing.TargetBucketID != targetBucketID || !existing.CapturedAt.Equal(capturedAt) {
			return state.ProjectEnvironmentCloneObjectManifest{}, state.ErrConflict
		}
		return existing, nil
	} else if !errors.Is(err, state.ErrNotFound) {
		return state.ProjectEnvironmentCloneObjectManifest{}, err
	}
	lister, ok := provider.(objectstorage.VersionedObjectLister)
	if !ok {
		return state.ProjectEnvironmentCloneObjectManifest{}, objectstorage.ErrUnsupported
	}
	versions, err := objectstorage.CaptureObjectManifest(ctx, lister, sourcePhysical, capturedAt, api.ObjectStorageInventoryMaxPages)
	if err != nil {
		return state.ProjectEnvironmentCloneObjectManifest{}, fmt.Errorf("capture source object versions: %w", err)
	}
	hash, err := objectstorage.ObjectManifestHash(versions)
	if err != nil {
		return state.ProjectEnvironmentCloneObjectManifest{}, err
	}
	manifest := state.ProjectEnvironmentCloneObjectManifest{
		OperationID: operationID, SourceBucketID: sourceBucketID, TargetBucketID: targetBucketID,
		CapturedAt: capturedAt, Hash: hash,
		Objects: make([]state.ProjectEnvironmentCloneObjectCheckpoint, len(versions)),
	}
	for i, version := range versions {
		manifest.Objects[i].Source = state.ProjectEnvironmentCloneObjectVersion{
			Key: version.Key, VersionID: version.VersionID, MetadataVersion: version.MetadataVersion,
			Size: version.Size, ETag: version.ETag, LastModified: version.LastModified,
			ValidUntil: version.ValidUntil, Deleted: version.Deleted,
		}
	}
	return store.PutProjectEnvironmentCloneObjectManifest(ctx, accountID, projectID, manifest)
}

func copyProjectEnvironmentObjectStorageSnapshot(
	ctx context.Context,
	store state.ProjectEnvironmentCloneObjectManifestStore,
	provider objectstorage.Provider,
	accountID, projectID, operationID, sourceBucketID, targetBucketID, sourcePhysical, targetPhysical string,
) (int, error) {
	manifest, err := store.ProjectEnvironmentCloneObjectManifest(ctx, accountID, projectID, operationID, sourceBucketID)
	if err != nil {
		return 0, err
	}
	if manifest.TargetBucketID != targetBucketID {
		return 0, state.ErrConflict
	}
	versions := make([]state.ProjectEnvironmentCloneObjectVersion, len(manifest.Objects))
	for i, item := range manifest.Objects {
		versions[i] = item.Source
	}
	hash, err := state.ProjectEnvironmentCloneObjectManifestHash(versions)
	if err != nil || hash != manifest.Hash {
		return 0, state.ErrConflict
	}
	copier, ok := provider.(objectstorage.ObjectSnapshotCopier)
	if !ok {
		return 0, objectstorage.ErrUnsupported
	}
	completed := 0
	for _, item := range manifest.Objects {
		if item.CopiedAt != nil {
			completed++
			continue
		}
		version := objectstorage.ObjectVersion{
			Key: item.Source.Key, VersionID: item.Source.VersionID, MetadataVersion: item.Source.MetadataVersion,
			Size: item.Source.Size, ETag: item.Source.ETag, LastModified: item.Source.LastModified,
			ValidUntil: item.Source.ValidUntil, Deleted: item.Source.Deleted,
		}
		verified, err := objectstorage.CopyAndVerifyObjectVersion(ctx, copier, sourcePhysical, targetPhysical, version)
		if err != nil {
			return completed, fmt.Errorf("copy pinned source object %q: %w", version.Key, err)
		}
		if err := store.MarkProjectEnvironmentCloneObjectCopied(ctx, accountID, projectID, operationID,
			sourceBucketID, version.Key, version.VersionID, verified.ETag, verified.SHA256); err != nil {
			return completed, err
		}
		completed++
	}
	return completed, nil
}
