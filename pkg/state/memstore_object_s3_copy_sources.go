package state

import (
	"context"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

var _ ObjectS3CopySourceStore = (*MemStore)(nil)

func copySourceGrantKey(credential, source string) string { return credential + "\x00" + source }

func (m *MemStore) ownedCopySourceCredentialLocked(account, bucket, credential string) (ObjectS3Credential, error) {
	c, ok := m.objectS3Credentials[credential]
	b, exists := m.objectBuckets[bucket]
	if !ok || !exists || c.AccountID != account || c.BucketID != bucket || b.AccountID != account || c.URL != nil || c.RotationParentID != "" {
		return ObjectS3Credential{}, ErrNotFound
	}
	return c, nil
}

func (m *MemStore) ListObjectS3CopySources(_ context.Context, account, bucket, credential string) ([]ObjectS3CopySource, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, err := m.ownedCopySourceCredentialLocked(account, bucket, credential); err != nil {
		return nil, err
	}
	out := []ObjectS3CopySource{}
	for _, g := range m.objectS3CopySources {
		if g.AccountID == account && g.CredentialID == credential {
			out = append(out, g)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SourceBucketID < out[j].SourceBucketID })
	return out, nil
}

func (m *MemStore) SetObjectS3CopySource(_ context.Context, account, bucket, credential, source, prefix string) (ObjectS3CopySource, error) {
	if !validObjectCopySourcePrefix(prefix) {
		return ObjectS3CopySource{}, ErrConflict
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	c, err := m.ownedCopySourceCredentialLocked(account, bucket, credential)
	if err != nil {
		return ObjectS3CopySource{}, err
	}
	b, exists := m.objectBuckets[source]
	if !exists || b.AccountID != account {
		return ObjectS3CopySource{}, ErrNotFound
	}
	if !validCopySourceCredential(c) || !compatibleCopySourceBuckets(m.objectBuckets[bucket], b) || m.activeDeletionLocked(bucket) || m.activeDeletionLocked(source) {
		return ObjectS3CopySource{}, ErrConflict
	}
	key := copySourceGrantKey(credential, source)
	g, exists := m.objectS3CopySources[key]
	if exists && g.Prefix == prefix {
		return g, nil
	}
	if !exists {
		count := 0
		for _, current := range m.objectS3CopySources {
			if current.CredentialID == credential {
				count++
			}
		}
		if count >= api.MaxObjectS3CopySourcesPerCredential {
			return ObjectS3CopySource{}, &ObjectStorageLimitError{Kind: "copy_sources_per_credential", Limit: api.MaxObjectS3CopySourcesPerCredential, Observed: int64(count + 1), Cause: ErrConflict}
		}
		g = ObjectS3CopySource{AccountID: account, CredentialID: credential, BucketID: bucket, SourceBucketID: source, CreatedAt: m.clock()}
	}
	g.ID, g.Prefix, g.UpdatedAt = uuid.NewString(), prefix, m.clock()
	if m.objectS3CopySources == nil {
		m.objectS3CopySources = map[string]ObjectS3CopySource{}
	}
	m.objectS3CopySources[key] = g
	return g, nil
}

func (m *MemStore) DeleteObjectS3CopySource(_ context.Context, account, bucket, credential, source string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, err := m.ownedCopySourceCredentialLocked(account, bucket, credential); err != nil {
		return err
	}
	key := copySourceGrantKey(credential, source)
	if _, exists := m.objectS3CopySources[key]; !exists {
		return ErrNotFound
	}
	delete(m.objectS3CopySources, key)
	return nil
}

func (m *MemStore) resolveObjectS3CopySourceLocked(account, credential, source, key string) (ObjectS3CopySource, ObjectBucket, error) {
	if key == "" || !validObjectCopySourcePrefix(key) {
		return ObjectS3CopySource{}, ObjectBucket{}, ErrNotFound
	}
	c, ok := m.objectS3Credentials[credential]
	if !ok || c.AccountID != account || !validCopySourceCredential(c) {
		return ObjectS3CopySource{}, ObjectBucket{}, ErrNotFound
	}
	owner := copySourceCredentialID(c)
	parent, ok := m.objectS3Credentials[owner]
	g, granted := m.objectS3CopySources[copySourceGrantKey(owner, source)]
	b, exists := m.objectBuckets[source]
	if !ok || !validCopySourceCredential(parent) || parent.AccountID != account || parent.BucketID != c.BucketID || !granted || !exists ||
		g.AccountID != account || !strings.HasPrefix(key, g.Prefix) || !compatibleCopySourceBuckets(m.objectBuckets[c.BucketID], b) ||
		m.activeDeletionLocked(source) || m.activeDeletionLocked(c.BucketID) {
		return ObjectS3CopySource{}, ObjectBucket{}, ErrNotFound
	}
	return g, b, nil
}

func (m *MemStore) ResolveObjectS3CopySource(_ context.Context, account, credential, source, key string) (ObjectS3CopySource, ObjectBucket, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.resolveObjectS3CopySourceLocked(account, credential, source, key)
}

func (m *MemStore) GetObjectS3CopySourceBucket(_ context.Context, account, id string) (ObjectBucket, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.objectBuckets[id]
	if !ok || b.AccountID != account || b.State == "deleted" {
		return ObjectBucket{}, ErrNotFound
	}
	return b, nil
}
