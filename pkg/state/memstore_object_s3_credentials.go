package state

import (
	"context"
	"sort"
	"time"

	"github.com/google/uuid"
)

var _ ObjectS3CredentialStore = (*MemStore)(nil)
var _ ObjectS3CredentialRekeyStore = (*MemStore)(nil)
var _ ObjectS3CredentialBindingStore = (*MemStore)(nil)

func (m *MemStore) CreateObjectS3Credential(_ context.Context, c ObjectS3Credential, maxPerBucket int) (ObjectS3Credential, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.createObjectS3CredentialLocked(c, maxPerBucket, false)
}

func (m *MemStore) createObjectS3CredentialLocked(c ObjectS3Credential, maxPerBucket int, cloneAuthorized bool) (ObjectS3Credential, error) {
	if !validObjectS3Credential(c) || maxPerBucket < 1 {
		return ObjectS3Credential{}, ErrConflict
	}
	bucket, ok := m.objectBuckets[c.BucketID]
	if !ok || bucket.AccountID != c.AccountID || bucket.State != "ready" || !cloneAuthorized && !m.cloneBucketAccessibleLocked(bucket) {
		return ObjectS3Credential{}, ErrNotFound
	}
	if m.objectS3Credentials == nil {
		m.objectS3Credentials = map[string]ObjectS3Credential{}
	}
	active := 0
	for _, existing := range m.objectS3Credentials {
		if existing.AccessKeyID == c.AccessKeyID || existing.ID == c.ID {
			return ObjectS3Credential{}, ErrConflict
		}
		if existing.BucketID == c.BucketID && existing.Status == ObjectS3CredentialStatusActive && existing.RotationParentID == "" {
			active++
		}
	}
	if active >= maxPerBucket {
		return ObjectS3Credential{}, ErrConflict
	}
	now := time.Now().UTC()
	c.CreatedAt, c.Status = now, ObjectS3CredentialStatusActive
	c.SecretSealed = append([]byte(nil), c.SecretSealed...)
	m.objectS3Credentials[c.ID] = c
	return cloneObjectS3Credential(c), nil
}

func (m *MemStore) CreateObjectS3ComputeBinding(_ context.Context, req ObjectS3ComputeBindingCreateRequest) (ObjectS3Credential, error) {
	if !validObjectS3ComputeBindingCreateRequest(req) {
		return ObjectS3Credential{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.createObjectS3ComputeBindingLocked(req, false)
}

func (m *MemStore) createObjectS3ComputeBindingLocked(req ObjectS3ComputeBindingCreateRequest, cloneAuthorized bool) (ObjectS3Credential, error) {
	c := req.Credential
	bucket, ok := m.objectBuckets[c.BucketID]
	if !ok || bucket.AccountID != c.AccountID || bucket.AppID != c.ManagedAppID || bucket.State != "ready" || !cloneAuthorized && !m.cloneBucketAccessibleLocked(bucket) {
		return ObjectS3Credential{}, ErrNotFound
	}
	app, ok := m.apps[c.ManagedAppID]
	if !ok || app.AccountID != c.AccountID {
		return ObjectS3Credential{}, ErrNotFound
	}
	active := 0
	for _, existing := range m.objectS3Credentials {
		if existing.AccessKeyID == c.AccessKeyID || existing.ID == c.ID ||
			(existing.BucketID == c.BucketID && existing.Status == ObjectS3CredentialStatusActive &&
				existing.ManagedAppID == c.ManagedAppID && existing.ManagedScope == c.ManagedScope && existing.ManagedPrefix == c.ManagedPrefix) {
			return ObjectS3Credential{}, ErrConflict
		}
		if existing.BucketID == c.BucketID && existing.Status == ObjectS3CredentialStatusActive && existing.RotationParentID == "" {
			active++
		}
	}
	if active >= req.MaxCredentialsPerBucket {
		return ObjectS3Credential{}, ErrConflict
	}
	secretCount := 0
	for _, existing := range m.secrets {
		if existing.AccountID == c.AccountID && existing.AppID == c.ManagedAppID {
			secretCount++
		}
	}
	if secretCount+len(req.Secrets) > req.MaxSecretsPerApp {
		return ObjectS3Credential{}, ErrConflict
	}
	for _, secret := range req.Secrets {
		if _, exists := m.secrets[secretKey{AppID: secret.AppID, Scope: secret.Scope, Key: secret.Key}]; exists {
			return ObjectS3Credential{}, ErrConflict
		}
	}
	if m.objectS3Credentials == nil {
		m.objectS3Credentials = map[string]ObjectS3Credential{}
	}
	now := time.Now().UTC()
	c.CreatedAt = now
	c.SecretSealed = append([]byte(nil), c.SecretSealed...)
	m.objectS3Credentials[c.ID] = c
	for _, secret := range req.Secrets {
		secret.Ciphertext = append([]byte(nil), secret.Ciphertext...)
		secret.CreatedAt, secret.UpdatedAt = now, now
		secret.DeliveryVersion, secret.DeliveryStatus = 1, SecretDeliveryPending
		m.secrets[secretKey{AppID: secret.AppID, Scope: secret.Scope, Key: secret.Key}] = secret
	}
	// A private clone has no running target yet. Publishing its release is the
	// visibility boundary; preparation must not invalidate production snapshots.
	if cloneAuthorized {
		return cloneObjectS3Credential(c), nil
	}
	if m.runtimeConfigChangedAt == nil {
		m.runtimeConfigChangedAt = map[string]time.Time{}
	}
	m.runtimeConfigChangedAt[c.ManagedAppID] = now
	for i := range m.snapshots {
		deployment, ok := m.deployments[m.snapshots[i].DeploymentID]
		if !ok || deployment.AppID != c.ManagedAppID || m.snapshots[i].Stale {
			continue
		}
		m.snapshots[i].Stale = true
		m.deleteSnapshotReplicasLocked(m.snapshots[i].ID)
	}
	return cloneObjectS3Credential(c), nil
}

func (m *MemStore) ListObjectS3Credentials(_ context.Context, accountID, bucketID string) ([]ObjectS3Credential, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	bucket, ok := m.objectBuckets[bucketID]
	if !ok || bucket.AccountID != accountID || bucket.State == "deleted" {
		return nil, ErrNotFound
	}
	out := make([]ObjectS3Credential, 0)
	for _, c := range m.objectS3Credentials {
		if c.AccountID == accountID && c.BucketID == bucketID && c.Status == ObjectS3CredentialStatusActive && c.RotationParentID == "" {
			out = append(out, cloneObjectS3Credential(c))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.Before(out[j].CreatedAt) || out[i].CreatedAt.Equal(out[j].CreatedAt) && out[i].ID < out[j].ID
	})
	return out, nil
}

func (m *MemStore) RevokeObjectS3Credential(_ context.Context, accountID, bucketID, credentialID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.objectS3Credentials[credentialID]
	if !ok || c.AccountID != accountID || c.BucketID != bucketID || c.Status != ObjectS3CredentialStatusActive {
		return ErrNotFound
	}
	now := time.Now().UTC()
	c.Status, c.RevokedAt = ObjectS3CredentialStatusRevoked, &now
	m.objectS3Credentials[credentialID] = c
	for id, stage := range m.objectS3Credentials {
		if stage.RotationParentID == credentialID && stage.Status == ObjectS3CredentialStatusActive {
			stage.Status, stage.RevokedAt = ObjectS3CredentialStatusRevoked, &now
			m.objectS3Credentials[id] = stage
		}
	}
	return nil
}

func (m *MemStore) RevokeObjectS3ComputeBinding(_ context.Context, accountID, bucketID, bindingID string) (bool, error) {
	if !validObjectS3ComputeBindingRevokeRequest(accountID, bucketID, bindingID) {
		return false, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.objectS3Credentials[bindingID]
	if !ok || c.AccountID != accountID || c.BucketID != bucketID || c.ManagedAppID == "" || c.RotationParentID != "" {
		return false, ErrNotFound
	}
	now := time.Now().UTC()
	changed := false
	if c.Status == ObjectS3CredentialStatusActive {
		c.Status, c.RevokedAt = ObjectS3CredentialStatusRevoked, &now
		m.objectS3Credentials[bindingID] = c
		changed = true
	}
	for id, stage := range m.objectS3Credentials {
		if stage.RotationParentID == bindingID && stage.Status == ObjectS3CredentialStatusActive {
			stage.Status, stage.RevokedAt = ObjectS3CredentialStatusRevoked, &now
			m.objectS3Credentials[id] = stage
			changed = true
		}
	}
	for key, secret := range m.secrets {
		if secret.ManagedObjectStorageCredentialID == bindingID {
			delete(m.secrets, key)
			changed = true
		}
	}
	if !changed {
		return false, nil
	}
	if m.runtimeConfigChangedAt == nil {
		m.runtimeConfigChangedAt = map[string]time.Time{}
	}
	if now.After(m.runtimeConfigChangedAt[c.ManagedAppID]) {
		m.runtimeConfigChangedAt[c.ManagedAppID] = now
	}
	for i := range m.snapshots {
		deployment, ok := m.deployments[m.snapshots[i].DeploymentID]
		if !ok || deployment.AppID != c.ManagedAppID || m.snapshots[i].Stale {
			continue
		}
		m.snapshots[i].Stale = true
		m.deleteSnapshotReplicasLocked(m.snapshots[i].ID)
	}
	return true, nil
}

func (m *MemStore) GetObjectS3Credential(_ context.Context, accountID, bucketID, credentialID string) (ObjectS3Credential, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.objectS3Credentials[credentialID]
	if !ok || c.AccountID != accountID || c.BucketID != bucketID || c.RotationParentID != "" {
		return ObjectS3Credential{}, ErrNotFound
	}
	return cloneObjectS3Credential(c), nil
}

func (m *MemStore) PendingObjectS3CredentialRotation(_ context.Context, accountID, bucketID, bindingID string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, c := range m.objectS3Credentials {
		if c.AccountID == accountID && c.BucketID == bucketID && c.RotationParentID == bindingID && c.Status == ObjectS3CredentialStatusActive {
			return c.RotationWakeID, nil
		}
	}
	return "", nil
}

func (m *MemStore) StageObjectS3CredentialRotation(_ context.Context, req ObjectS3CredentialRotationRequest) (ObjectS3Credential, error) {
	if !validObjectS3CredentialRotationRequest(req) {
		return ObjectS3Credential{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.objectS3Credentials[req.BindingID]
	if !ok || c.AccountID != req.AccountID || c.BucketID != req.BucketID || c.Status != ObjectS3CredentialStatusActive || c.RotationParentID != "" || c.ManagedAppID == "" {
		return ObjectS3Credential{}, ErrNotFound
	}
	if err := validateObjectS3CredentialRotationSecrets(req, c); err != nil {
		return ObjectS3Credential{}, err
	}
	for _, existing := range m.objectS3Credentials {
		if (existing.RotationParentID == req.BindingID && existing.Status == ObjectS3CredentialStatusActive) || existing.AccessKeyID == req.AccessKeyID {
			return ObjectS3Credential{}, ErrConflict
		}
	}
	for _, secret := range req.Secrets {
		row, ok := m.secrets[secretKey{AppID: secret.AppID, Scope: secret.Scope, Key: secret.Key}]
		if !ok || row.AccountID != secret.AccountID || row.ManagedObjectStorageCredentialID != req.BindingID {
			return ObjectS3Credential{}, ErrConflict
		}
	}
	stage := cloneObjectS3Credential(c)
	stage.ID = uuid.NewString()
	stage.ManagedAppID, stage.ManagedScope, stage.ManagedPrefix = "", "", ""
	stage.RotationParentID, stage.RotationWakeID = req.BindingID, req.WakeID
	stage.CreatedAt = time.Now().UTC()
	stage.LastUsedAt = nil
	m.objectS3Credentials[stage.ID] = stage
	c.AccessKeyID, c.SecretSealed, c.KID = req.AccessKeyID, append([]byte(nil), req.SecretSealed...), req.KID
	c.LastUsedAt = nil
	m.objectS3Credentials[req.BindingID] = c
	for _, secret := range req.Secrets {
		key := secretKey{AppID: secret.AppID, Scope: secret.Scope, Key: secret.Key}
		row := m.secrets[key]
		row.Ciphertext = append([]byte(nil), secret.Ciphertext...)
		row.Kid, row.ValueHash = secret.Kid, secret.ValueHash
		row.DeliveryVersion++
		row.DeliveryStatus = SecretDeliveryPending
		row.LastDeliveryAttemptAt, row.LastDeliveryErrorCode = nil, ""
		row.UpdatedAt = time.Now().UTC()
		m.secrets[key] = row
	}
	return cloneObjectS3Credential(c), nil
}

func (m *MemStore) StampObjectS3CredentialRotation(_ context.Context, appID, wakeID string) error {
	if appID == "" || wakeID == "" {
		return ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, stage := range m.objectS3Credentials {
		if stage.RotationWakeID != wakeID || stage.Status != ObjectS3CredentialStatusActive || stage.RotationStampedAt != nil {
			continue
		}
		parent, ok := m.objectS3Credentials[stage.RotationParentID]
		if !ok || parent.ManagedAppID != appID || parent.Status != ObjectS3CredentialStatusActive {
			continue
		}
		now := time.Now().UTC()
		stage.RotationStampedAt = &now
		m.objectS3Credentials[id] = stage
		if m.runtimeConfigChangedAt == nil {
			m.runtimeConfigChangedAt = map[string]time.Time{}
		}
		if now.After(m.runtimeConfigChangedAt[appID]) {
			m.runtimeConfigChangedAt[appID] = now
		}
	}
	return nil
}

func (m *MemStore) FinalizeObjectS3CredentialRotationsForApp(_ context.Context, appID, wakeID string) error {
	if appID == "" || wakeID == "" {
		return ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC()
	for id, stage := range m.objectS3Credentials {
		if stage.RotationWakeID != wakeID || stage.Status != ObjectS3CredentialStatusActive {
			continue
		}
		parent, ok := m.objectS3Credentials[stage.RotationParentID]
		if !ok || parent.ManagedAppID != appID || parent.Status != ObjectS3CredentialStatusActive {
			continue
		}
		stage.Status, stage.RevokedAt = ObjectS3CredentialStatusRevoked, &now
		m.objectS3Credentials[id] = stage
	}
	return nil
}

func (m *MemStore) ResolveObjectS3Credential(_ context.Context, accessKeyID string) (ObjectS3Credential, ObjectBucket, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, c := range m.objectS3Credentials {
		if c.AccessKeyID != accessKeyID || c.Status != ObjectS3CredentialStatusActive {
			continue
		}
		bucket, ok := m.objectBuckets[c.BucketID]
		if !ok || bucket.AccountID != c.AccountID || bucket.State != "ready" || !m.cloneBucketAccessibleLocked(bucket) {
			return ObjectS3Credential{}, ObjectBucket{}, ErrNotFound
		}
		return cloneObjectS3Credential(c), bucket, nil
	}
	return ObjectS3Credential{}, ObjectBucket{}, ErrNotFound
}

func (m *MemStore) TouchObjectS3Credential(_ context.Context, credentialID string, usedAt time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.objectS3Credentials[credentialID]
	if !ok || c.Status != ObjectS3CredentialStatusActive {
		return ErrNotFound
	}
	usedAt = usedAt.UTC()
	if c.LastUsedAt == nil || usedAt.Sub(*c.LastUsedAt) >= time.Minute {
		c.LastUsedAt = &usedAt
		m.objectS3Credentials[credentialID] = c
	}
	return nil
}

func (m *MemStore) ListObjectS3CredentialsForRekey(_ context.Context, limit int, afterID string) ([]ObjectS3Credential, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if limit < 1 || limit > 200 {
		limit = 50
	}
	out := make([]ObjectS3Credential, 0, limit)
	for _, c := range m.objectS3Credentials {
		if c.Status == ObjectS3CredentialStatusActive && c.ID > afterID {
			out = append(out, cloneObjectS3Credential(c))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *MemStore) ResealObjectS3Credential(_ context.Context, credentialID, previousKID, currentKID string, sealed []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if currentKID == "" || len(sealed) == 0 {
		return ErrInvalidArgument
	}
	c, ok := m.objectS3Credentials[credentialID]
	if !ok || c.Status != ObjectS3CredentialStatusActive || c.KID != previousKID {
		return ErrNotFound
	}
	c.KID = currentKID
	c.SecretSealed = append([]byte(nil), sealed...)
	m.objectS3Credentials[credentialID] = c
	return nil
}

func cloneObjectS3Credential(c ObjectS3Credential) ObjectS3Credential {
	c.SecretSealed = append([]byte(nil), c.SecretSealed...)
	if c.LastUsedAt != nil {
		v := *c.LastUsedAt
		c.LastUsedAt = &v
	}
	if c.RevokedAt != nil {
		v := *c.RevokedAt
		c.RevokedAt = &v
	}
	if c.RotationStampedAt != nil {
		v := *c.RotationStampedAt
		c.RotationStampedAt = &v
	}
	return c
}
