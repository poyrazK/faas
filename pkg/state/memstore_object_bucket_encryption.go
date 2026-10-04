package state

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
)

var _ ObjectBucketEncryptionStore = (*MemStore)(nil)

func (m *MemStore) saveObjectBucketEncryptionLocked(j ObjectBucketEncryption) {
	if m.objectBucketEncryption == nil {
		m.objectBucketEncryption = map[string]ObjectBucketEncryption{}
	}
	m.objectBucketEncryption[j.BucketID] = cloneObjectBucketEncryption(j)
}

func (m *MemStore) ownedObjectBucketEncryptionLocked(account, app, bucket string) (ObjectBucketEncryption, error) {
	b, ok := m.objectBuckets[bucket]
	if !ok || b.AccountID != account || b.AppID != app {
		return ObjectBucketEncryption{}, ErrNotFound
	}
	j, ok := m.objectBucketEncryption[bucket]
	if !ok {
		j = newObjectBucketEncryption(b, m.clock())
	}
	return cloneObjectBucketEncryption(j), nil
}

func (m *MemStore) GetObjectBucketEncryption(_ context.Context, account, app, bucket string) (ObjectBucketEncryption, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.ownedObjectBucketEncryptionLocked(account, app, bucket)
}

func (m *MemStore) RequestObjectBucketEncryption(_ context.Context, account, app, bucket string, desired ObjectEncryptionSnapshot) (ObjectBucketEncryption, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, err := m.ownedObjectBucketEncryptionLocked(account, app, bucket)
	if err != nil {
		return j, err
	}
	if m.objectBuckets[bucket].State != "ready" || m.activeDeletionLocked(bucket) {
		return j, ErrConflict
	}
	j, err = requestObjectBucketEncryption(j, desired, m.clock())
	if err == nil {
		m.saveObjectBucketEncryptionLocked(j)
	}
	return cloneObjectBucketEncryption(j), err
}

func (m *MemStore) DueObjectBucketEncryption(_ context.Context, limit int32) ([]ObjectBucketEncryption, error) {
	if limit < 1 || limit > api.ObjectBucketEncryptionBatch {
		return nil, ErrConflict
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	jobs := []ObjectBucketEncryption{}
	for _, j := range m.objectBucketEncryption {
		if j.State != "ready" && !j.LeaseUntil.After(m.clock()) && !j.RetryAt.After(m.clock()) {
			jobs = append(jobs, cloneObjectBucketEncryption(j))
		}
	}
	return orderObjectBucketEncryptionJobs(jobs, limit), nil
}

func (m *MemStore) RefreshObjectBucketEncryption(_ context.Context, account, app, bucket string, revision int64) (ObjectBucketEncryption, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, err := m.ownedObjectBucketEncryptionLocked(account, app, bucket); err != nil {
		return ObjectBucketEncryption{}, err
	}
	return m.mutateObjectBucketEncryptionLocked(bucket, func(j ObjectBucketEncryption) (ObjectBucketEncryption, error) {
		return refreshObjectBucketEncryption(j, revision, m.clock())
	})
}

func (m *MemStore) mutateObjectBucketEncryptionLocked(bucket string, fn func(ObjectBucketEncryption) (ObjectBucketEncryption, error)) (ObjectBucketEncryption, error) {
	j, ok := m.objectBucketEncryption[bucket]
	if !ok {
		return j, ErrNotFound
	}
	if m.objectBuckets[bucket].State != "ready" || m.activeDeletionLocked(bucket) {
		return j, ErrConflict
	}
	j, err := fn(cloneObjectBucketEncryption(j))
	if err == nil {
		m.saveObjectBucketEncryptionLocked(j)
	}
	return cloneObjectBucketEncryption(j), err
}

func (m *MemStore) ClaimObjectBucketEncryption(_ context.Context, bucket, token string) (ObjectBucketEncryption, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.mutateObjectBucketEncryptionLocked(bucket, func(j ObjectBucketEncryption) (ObjectBucketEncryption, error) {
		return claimObjectBucketEncryption(j, token, m.clock())
	})
}

func (m *MemStore) DispatchObjectBucketEncryption(_ context.Context, bucket, token string) (ObjectBucketEncryption, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.mutateObjectBucketEncryptionLocked(bucket, func(j ObjectBucketEncryption) (ObjectBucketEncryption, error) {
		if !validObjectBucketEncryptionLease(j, token, m.clock()) {
			return j, ErrConflict
		}
		j.Dispatched, j.UpdatedAt = true, m.clock()
		return j, nil
	})
}

func (m *MemStore) FinishObjectBucketEncryption(_ context.Context, bucket, token string, verified ObjectEncryptionSnapshot) (ObjectBucketEncryption, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.mutateObjectBucketEncryptionLocked(bucket, func(j ObjectBucketEncryption) (ObjectBucketEncryption, error) {
		return finishObjectBucketEncryption(j, token, verified, m.clock())
	})
}

func (m *MemStore) RetryObjectBucketEncryption(_ context.Context, bucket, token string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, err := m.mutateObjectBucketEncryptionLocked(bucket, func(j ObjectBucketEncryption) (ObjectBucketEncryption, error) {
		return retryObjectBucketEncryption(j, token, m.clock())
	})
	return err
}

func (m *MemStore) captureObjectBucketDefaultLocked(bucket string, e ObjectEncryptionSnapshot) (ObjectEncryptionSnapshot, int64, error) {
	return captureObjectBucketDefault(m.objectBucketEncryption[bucket], e)
}

func (m *MemStore) objectBucketDefaultRequiresTrackingLocked(bucket string) bool {
	j := m.objectBucketEncryption[bucket]
	return j.State != "" && j.State != "ready" || !j.Encryption.Empty()
}
