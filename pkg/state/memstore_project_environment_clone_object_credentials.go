package state

import (
	"context"
	"time"
)

func (m *MemStore) cloneObjectCredentialContextLocked(lease ProjectEnvironmentCloneLease) (ProjectEnvironmentCloneOperation, []ProjectEnvironmentCloneBindings, error) {
	op, _, err := m.cloneLeaseOwnedLocked(lease, time.Now().UTC())
	if err != nil {
		return op, nil, err
	}
	if op.Status != CloneOperationCopying && op.Status != CloneOperationPublishing {
		return op, nil, ErrConflict
	}
	records := make([]projectCloneWorkloadRecord, 0, len(m.projectEnvironmentCloneWorkloads[op.ID]))
	for _, record := range m.projectEnvironmentCloneWorkloads[op.ID] {
		records = append(records, record)
	}
	views, err := cloneBindingViews(records)
	return op, views, err
}

func (m *MemStore) validateCloneCredentialBucketLocked(op ProjectEnvironmentCloneOperation, views []ProjectEnvironmentCloneBindings, request ProjectEnvironmentCloneObjectCredentialRequest) error {
	want, source, err := capturedCloneBucketReservation(op, views, request.AppID, request.SourceBucketID)
	if err != nil {
		return err
	}
	bucket, exists := m.objectBuckets[request.Target.Credential.BucketID]
	if !exists {
		return ErrNotFound
	}
	if err := validateCloneBucketReservation(want, bucket, source); err != nil {
		return err
	}
	if bucket.State != "ready" {
		return ErrConflict
	}
	manifest, exists := m.projectEnvironmentCloneObjectManifests[cloneObjectManifestKey(op.ID, source.ID)]
	if !exists {
		return ErrNotFound
	}
	return validateCloneObjectCredentialCopy(op, source.ID, bucket.ID, manifest)
}

func (m *MemStore) verifyPreparedCloneObjectCredentialLocked(prepared ProjectEnvironmentCloneObjectCredentialPreparation) (ProjectEnvironmentCloneObjectCredentialPreparation, error) {
	actual := prepared
	credential, exists := m.objectS3Credentials[prepared.Credential.ID]
	if !exists {
		return prepared, ErrNotFound
	}
	actual.Credential, actual.Secrets = credential, nil
	for _, secret := range m.secrets {
		if secret.ManagedObjectStorageCredentialID == credential.ID {
			actual.Secrets = append(actual.Secrets, secret)
		}
	}
	normalized, _, err := normalizeCloneObjectCredentialPreparation(actual)
	if err != nil || normalized.Hash != prepared.Hash {
		return prepared, ErrConflict
	}
	return normalized, nil
}

func (m *MemStore) PrepareProjectEnvironmentCloneObjectCredential(_ context.Context, lease ProjectEnvironmentCloneLease, request ProjectEnvironmentCloneObjectCredentialRequest) (ProjectEnvironmentCloneObjectCredentialPreparation, error) {
	if !validCloneLeaseIdentity(lease) {
		return ProjectEnvironmentCloneObjectCredentialPreparation{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	op, views, err := m.cloneObjectCredentialContextLocked(lease)
	if err != nil {
		return ProjectEnvironmentCloneObjectCredentialPreparation{}, err
	}
	if op.Status != CloneOperationCopying {
		return ProjectEnvironmentCloneObjectCredentialPreparation{}, ErrConflict
	}
	if err := validateCloneObjectCredentialRequest(op, views, request); err != nil {
		return ProjectEnvironmentCloneObjectCredentialPreparation{}, err
	}
	if err := m.validateCloneCredentialBucketLocked(op, views, request); err != nil {
		return ProjectEnvironmentCloneObjectCredentialPreparation{}, err
	}
	prepared, _, err := newCloneObjectCredentialPreparation(op, request)
	if err != nil {
		return prepared, err
	}
	key := cloneObjectManifestKey(op.ID, request.SourceCredentialID)
	if existing, found := m.projectEnvironmentCloneObjectCredentials[key]; found {
		if existing.Hash != prepared.Hash {
			return ProjectEnvironmentCloneObjectCredentialPreparation{}, ErrConflict
		}
		return m.verifyPreparedCloneObjectCredentialLocked(existing)
	}
	if request.Target.Credential.ManagedAppID == "" {
		_, err = m.createObjectS3CredentialLocked(request.Target.Credential, request.Target.MaxCredentialsPerBucket, true)
	} else {
		_, err = m.createObjectS3ComputeBindingLocked(request.Target, true)
	}
	if err != nil {
		return ProjectEnvironmentCloneObjectCredentialPreparation{}, err
	}
	m.projectEnvironmentCloneObjectCredentials[key] = prepared
	return m.verifyPreparedCloneObjectCredentialLocked(prepared)
}

func (m *MemStore) ProjectEnvironmentCloneObjectCredentialForLease(_ context.Context, lease ProjectEnvironmentCloneLease, sourceCredentialID string) (ProjectEnvironmentCloneObjectCredentialPreparation, error) {
	if !validCloneLeaseIdentity(lease) || !validCloneCredentialSourceID(sourceCredentialID) {
		return ProjectEnvironmentCloneObjectCredentialPreparation{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	op, views, err := m.cloneObjectCredentialContextLocked(lease)
	if err != nil {
		return ProjectEnvironmentCloneObjectCredentialPreparation{}, err
	}
	prepared, found := m.projectEnvironmentCloneObjectCredentials[cloneObjectManifestKey(op.ID, sourceCredentialID)]
	if !found {
		return ProjectEnvironmentCloneObjectCredentialPreparation{}, ErrNotFound
	}
	request := cloneObjectCredentialReplayRequest(prepared)
	if err := validateCloneObjectCredentialRequest(op, views, request); err != nil {
		return ProjectEnvironmentCloneObjectCredentialPreparation{}, err
	}
	if err := m.validateCloneCredentialBucketLocked(op, views, request); err != nil {
		return ProjectEnvironmentCloneObjectCredentialPreparation{}, err
	}
	return m.verifyPreparedCloneObjectCredentialLocked(prepared)
}

func cloneObjectCredentialReplayRequest(prepared ProjectEnvironmentCloneObjectCredentialPreparation) ProjectEnvironmentCloneObjectCredentialRequest {
	return ProjectEnvironmentCloneObjectCredentialRequest{AppID: prepared.AppID, SourceBucketID: prepared.SourceBucketID, SourceCredentialID: prepared.SourceCredentialID,
		Target: ObjectS3ComputeBindingCreateRequest{Credential: prepared.Credential, Secrets: prepared.Secrets, MaxCredentialsPerBucket: 1, MaxSecretsPerApp: 6}}
}
