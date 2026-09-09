package state

import (
	"context"
	"sort"
	"time"
)

var _ ObjectS3CredentialStore = (*MemStore)(nil)
var _ ObjectS3CredentialRekeyStore = (*MemStore)(nil)
var _ ObjectS3CredentialBindingStore = (*MemStore)(nil)

func (m *MemStore) CreateObjectS3Credential(_ context.Context, c ObjectS3Credential, maxPerBucket int) (ObjectS3Credential, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !validObjectS3Credential(c) || maxPerBucket < 1 {
		return ObjectS3Credential{}, ErrConflict
	}
	bucket, ok := m.objectBuckets[c.BucketID]
	if !ok || bucket.AccountID != c.AccountID || bucket.State != "ready" {
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
		if existing.BucketID == c.BucketID && existing.Status == ObjectS3CredentialStatusActive {
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

func (m *MemStore) ListObjectS3Credentials(_ context.Context, accountID, bucketID string) ([]ObjectS3Credential, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	bucket, ok := m.objectBuckets[bucketID]
	if !ok || bucket.AccountID != accountID || bucket.State == "deleted" {
		return nil, ErrNotFound
	}
	out := make([]ObjectS3Credential, 0)
	for _, c := range m.objectS3Credentials {
		if c.AccountID == accountID && c.BucketID == bucketID && c.Status == ObjectS3CredentialStatusActive {
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
	return nil
}

func (m *MemStore) GetObjectS3Credential(_ context.Context, accountID, bucketID, credentialID string) (ObjectS3Credential, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.objectS3Credentials[credentialID]
	if !ok || c.AccountID != accountID || c.BucketID != bucketID {
		return ObjectS3Credential{}, ErrNotFound
	}
	return cloneObjectS3Credential(c), nil
}

func (m *MemStore) RotateObjectS3Credential(_ context.Context, accountID, bucketID, credentialID, accessKeyID string, sealed []byte, kid string) (ObjectS3Credential, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if accessKeyID == "" || len(sealed) == 0 || kid == "" {
		return ObjectS3Credential{}, ErrInvalidArgument
	}
	c, ok := m.objectS3Credentials[credentialID]
	if !ok || c.AccountID != accountID || c.BucketID != bucketID || c.Status != ObjectS3CredentialStatusActive {
		return ObjectS3Credential{}, ErrNotFound
	}
	for id, existing := range m.objectS3Credentials {
		if id != credentialID && existing.AccessKeyID == accessKeyID {
			return ObjectS3Credential{}, ErrConflict
		}
	}
	c.AccessKeyID, c.SecretSealed, c.KID = accessKeyID, append([]byte(nil), sealed...), kid
	c.LastUsedAt = nil
	m.objectS3Credentials[credentialID] = c
	return cloneObjectS3Credential(c), nil
}

func (m *MemStore) ResolveObjectS3Credential(_ context.Context, accessKeyID string) (ObjectS3Credential, ObjectBucket, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, c := range m.objectS3Credentials {
		if c.AccessKeyID != accessKeyID || c.Status != ObjectS3CredentialStatusActive {
			continue
		}
		bucket, ok := m.objectBuckets[c.BucketID]
		if !ok || bucket.AccountID != c.AccountID || bucket.State != "ready" {
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
	return c
}
