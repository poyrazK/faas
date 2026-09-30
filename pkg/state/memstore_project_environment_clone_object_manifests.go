package state

import (
	"context"
	"time"
)

func cloneObjectManifestKey(operationID, bucketID string) string {
	return operationID + ":" + bucketID
}

func (m *MemStore) PutProjectEnvironmentCloneObjectManifest(_ context.Context, accountID, projectID string, manifest ProjectEnvironmentCloneObjectManifest) (ProjectEnvironmentCloneObjectManifest, error) {
	return m.putProjectEnvironmentCloneObjectManifest(accountID, projectID, manifest, nil)
}

func (m *MemStore) PutProjectEnvironmentCloneObjectManifestForLease(_ context.Context, lease ProjectEnvironmentCloneLease, manifest ProjectEnvironmentCloneObjectManifest) (ProjectEnvironmentCloneObjectManifest, error) {
	if !validCloneLeaseIdentity(lease) || manifest.OperationID != lease.Operation.ID {
		return ProjectEnvironmentCloneObjectManifest{}, ErrInvalidArgument
	}
	return m.putProjectEnvironmentCloneObjectManifest(lease.Operation.AccountID, lease.Operation.ProjectID, manifest, &lease)
}

func (m *MemStore) authorizeCloneObjectMutationLocked(op ProjectEnvironmentCloneOperation, lease *ProjectEnvironmentCloneLease) error {
	if lease == nil {
		owner := m.projectEnvironmentCloneWorkerLeases[op.ID]
		if owner.attemptCount != 0 || owner.token != "" {
			return ErrConflict
		}
		return nil
	}
	_, _, err := m.cloneLeaseOwnedLocked(*lease, time.Now().UTC())
	return err
}

func (m *MemStore) putProjectEnvironmentCloneObjectManifest(accountID, projectID string, manifest ProjectEnvironmentCloneObjectManifest, lease *ProjectEnvironmentCloneLease) (ProjectEnvironmentCloneObjectManifest, error) {
	if err := validateProjectEnvironmentCloneObjectManifest(manifest); err != nil {
		return ProjectEnvironmentCloneObjectManifest{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	op, ok := m.projectEnvironmentCloneOperations[manifest.OperationID]
	if !ok || op.AccountID != accountID || op.ProjectID != projectID {
		return ProjectEnvironmentCloneObjectManifest{}, ErrNotFound
	}
	if err := m.authorizeCloneObjectMutationLocked(op, lease); err != nil {
		return ProjectEnvironmentCloneObjectManifest{}, err
	}
	if (op.Status != CloneOperationCapturing && op.Status != CloneOperationCopying) || !m.cloneOperationLeaseLiveLocked(op.ID) {
		return ProjectEnvironmentCloneObjectManifest{}, ErrConflict
	}
	key := cloneObjectManifestKey(manifest.OperationID, manifest.SourceBucketID)
	if existing, found := m.projectEnvironmentCloneObjectManifests[key]; found {
		if existing.TargetBucketID != manifest.TargetBucketID || existing.Hash != manifest.Hash ||
			!existing.CapturedAt.Equal(manifest.CapturedAt) || len(existing.Objects) != len(manifest.Objects) {
			return ProjectEnvironmentCloneObjectManifest{}, ErrConflict
		}
		return cloneProjectEnvironmentCloneObjectManifest(existing), nil
	}
	if op.Status != CloneOperationCapturing {
		return ProjectEnvironmentCloneObjectManifest{}, ErrConflict
	}
	stored := cloneProjectEnvironmentCloneObjectManifest(manifest)
	m.projectEnvironmentCloneObjectManifests[key] = stored
	return cloneProjectEnvironmentCloneObjectManifest(stored), nil
}

func (m *MemStore) ProjectEnvironmentCloneObjectManifest(_ context.Context, accountID, projectID, operationID, sourceBucketID string) (ProjectEnvironmentCloneObjectManifest, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	op, ok := m.projectEnvironmentCloneOperations[operationID]
	if !ok || op.AccountID != accountID || op.ProjectID != projectID {
		return ProjectEnvironmentCloneObjectManifest{}, ErrNotFound
	}
	manifest, found := m.projectEnvironmentCloneObjectManifests[cloneObjectManifestKey(operationID, sourceBucketID)]
	if !found {
		return ProjectEnvironmentCloneObjectManifest{}, ErrNotFound
	}
	return cloneProjectEnvironmentCloneObjectManifest(manifest), nil
}

func (m *MemStore) MarkProjectEnvironmentCloneObjectCopied(_ context.Context, accountID, projectID, operationID, sourceBucketID, key, sourceVersion, targetETag, verifiedSHA256 string) error {
	return m.markProjectEnvironmentCloneObjectCopied(accountID, projectID, operationID, sourceBucketID, key, sourceVersion, targetETag, verifiedSHA256, nil)
}

func (m *MemStore) MarkProjectEnvironmentCloneObjectCopiedForLease(_ context.Context, lease ProjectEnvironmentCloneLease, sourceBucketID, key, sourceVersion, targetETag, verifiedSHA256 string) error {
	if !validCloneLeaseIdentity(lease) {
		return ErrInvalidArgument
	}
	return m.markProjectEnvironmentCloneObjectCopied(lease.Operation.AccountID, lease.Operation.ProjectID, lease.Operation.ID, sourceBucketID, key, sourceVersion, targetETag, verifiedSHA256, &lease)
}

func (m *MemStore) markProjectEnvironmentCloneObjectCopied(accountID, projectID, operationID, sourceBucketID, key, sourceVersion, targetETag, verifiedSHA256 string, lease *ProjectEnvironmentCloneLease) error {
	if !validCloneObjectKey(key) || sourceVersion == "" || targetETag == "" || !validCloneObjectSHA256(verifiedSHA256) {
		return ErrInvalidProjectEnvironmentCloneOperation
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	op, ok := m.projectEnvironmentCloneOperations[operationID]
	if !ok || op.AccountID != accountID || op.ProjectID != projectID {
		return ErrNotFound
	}
	if err := m.authorizeCloneObjectMutationLocked(op, lease); err != nil {
		return err
	}
	if op.Status != CloneOperationCopying || !m.cloneOperationLeaseLiveLocked(op.ID) {
		return ErrConflict
	}
	manifestKey := cloneObjectManifestKey(operationID, sourceBucketID)
	manifest, found := m.projectEnvironmentCloneObjectManifests[manifestKey]
	if !found {
		return ErrNotFound
	}
	for i, item := range manifest.Objects {
		if item.Source.Key != key {
			continue
		}
		if item.Source.VersionID != sourceVersion ||
			(item.CopiedAt != nil && (item.TargetETag != targetETag || item.VerifiedSHA256 != verifiedSHA256)) {
			return ErrConflict
		}
		if item.CopiedAt == nil {
			now := time.Now().UTC()
			item.CopiedAt = &now
			item.TargetETag = targetETag
			item.VerifiedSHA256 = verifiedSHA256
			manifest.Objects[i] = item
			m.projectEnvironmentCloneObjectManifests[manifestKey] = manifest
		}
		return nil
	}
	return ErrNotFound
}
