package state

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
)

var _ ObjectBucketObjectLockStore = (*MemStore)(nil)

func (m *MemStore) saveObjectBucketObjectLockLocked(j ObjectBucketObjectLock) {
	if m.objectBucketObjectLock == nil {
		m.objectBucketObjectLock = map[string]ObjectBucketObjectLock{}
	}
	m.objectBucketObjectLock[j.BucketID] = cloneObjectBucketObjectLock(j)
}

func (m *MemStore) ownedObjectBucketObjectLockLocked(account, app, bucket string) (ObjectBucketObjectLock, error) {
	b, ok := m.objectBuckets[bucket]
	if !ok || b.AccountID != account || b.AppID != app || b.State == "deleted" {
		return ObjectBucketObjectLock{}, ErrNotFound
	}
	j, ok := m.objectBucketObjectLock[bucket]
	if !ok {
		j = newObjectBucketObjectLock(b, m.clock())
	}
	return cloneObjectBucketObjectLock(j), nil
}

func (m *MemStore) GetObjectBucketObjectLock(_ context.Context, account, app, bucket string) (ObjectBucketObjectLock, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.ownedObjectBucketObjectLockLocked(account, app, bucket)
}

func (m *MemStore) RequestObjectBucketObjectLock(_ context.Context, account, app, bucket string, c api.ObjectBucketObjectLockConfiguration) (ObjectBucketObjectLock, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, err := m.ownedObjectBucketObjectLockLocked(account, app, bucket)
	if err != nil {
		return j, err
	}
	if j.DesiredConfiguration != nil && j.DesiredConfiguration.Equal(c) {
		return j, nil
	}
	if m.objectBuckets[bucket].State != "ready" || m.activeDeletionLocked(bucket) || m.activeCapacityLocked(bucket) {
		return j, ErrConflict
	}
	_, unsafe, _, _ := m.capacityReadinessLocked(bucket)
	if unsafe {
		return j, ErrConflict
	}
	v, err := m.ownedObjectVersioningLocked(account, app, bucket)
	if err != nil {
		return j, err
	}
	j, err = requestObjectBucketObjectLock(j, c, m.clock())
	if err != nil {
		return j, err
	}
	v, err = requireObjectLockVersioning(v, j, m.clock())
	if err != nil {
		return j, err
	}
	m.saveObjectVersioningLocked(v)
	m.saveObjectBucketObjectLockLocked(j)
	return cloneObjectBucketObjectLock(j), nil
}

func (m *MemStore) ObserveObjectBucketObjectLock(_ context.Context, account, app, bucket string, revision int64, c api.ObjectBucketObjectLockConfiguration, known bool) (ObjectBucketObjectLock, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, err := m.ownedObjectBucketObjectLockLocked(account, app, bucket)
	if err != nil {
		return j, err
	}
	v, err := m.ownedObjectVersioningLocked(account, app, bucket)
	if err != nil {
		return j, err
	}
	j, err = observeObjectBucketObjectLock(j, revision, c, known, objectLockVersioningReady(v), m.clock())
	if err != nil {
		return j, err
	}
	if !m.activeDeletionLocked(bucket) {
		v, err = requireObjectLockVersioning(v, j, m.clock())
		if err != nil {
			return j, err
		}
		if j.EnabledRequired {
			m.saveObjectVersioningLocked(v)
		}
	}
	m.saveObjectBucketObjectLockLocked(j)
	return cloneObjectBucketObjectLock(j), nil
}

func (m *MemStore) DueObjectBucketObjectLock(_ context.Context, limit int32) ([]ObjectBucketObjectLock, error) {
	if limit < 1 || limit > api.ObjectBucketObjectLockBatch {
		return nil, ErrConflict
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	jobs := []ObjectBucketObjectLock{}
	for _, j := range m.objectBucketObjectLock {
		if objectLockActive(j) && !j.LeaseUntil.After(m.clock()) && !j.RetryAt.After(m.clock()) {
			jobs = append(jobs, cloneObjectBucketObjectLock(j))
		}
	}
	return orderObjectBucketObjectLockJobs(jobs, limit), nil
}

func (m *MemStore) mutateObjectBucketObjectLockLocked(bucket string, fn func(ObjectBucketObjectLock, ObjectBucketVersioning) (ObjectBucketObjectLock, error)) (ObjectBucketObjectLock, error) {
	j, ok := m.objectBucketObjectLock[bucket]
	if !ok {
		return j, ErrNotFound
	}
	if m.objectBuckets[bucket].State != "ready" {
		return j, ErrConflict
	}
	v, err := m.ownedObjectVersioningLocked(j.AccountID, j.AppID, bucket)
	if err != nil {
		return j, err
	}
	j, err = fn(cloneObjectBucketObjectLock(j), v)
	if err == nil {
		m.saveObjectBucketObjectLockLocked(j)
	}
	return cloneObjectBucketObjectLock(j), err
}

func (m *MemStore) ClaimObjectBucketObjectLock(_ context.Context, bucket, token string) (ObjectBucketObjectLock, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.mutateObjectBucketObjectLockLocked(bucket, func(j ObjectBucketObjectLock, v ObjectBucketVersioning) (ObjectBucketObjectLock, error) {
		pending, unsafe, multipart, _ := m.capacityReadinessLocked(bucket)
		return claimObjectBucketObjectLock(j, token, objectLockVersioningReady(v), pending, unsafe, multipart, m.activeCapacityLocked(bucket), m.clock())
	})
}

func (m *MemStore) DispatchObjectBucketObjectLock(_ context.Context, bucket, token string) (ObjectBucketObjectLock, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.mutateObjectBucketObjectLockLocked(bucket, func(j ObjectBucketObjectLock, v ObjectBucketVersioning) (ObjectBucketObjectLock, error) {
		pending, unsafe, multipart, _ := m.capacityReadinessLocked(bucket)
		if !validObjectLockLease(j, token, m.clock()) || j.DesiredConfiguration == nil || !objectLockVersioningReady(v) || pending > 0 || unsafe || multipart || m.activeCapacityLocked(bucket) {
			return j, ErrConflict
		}
		j.Dispatched, j.UpdatedAt = true, m.clock()
		return j, nil
	})
}

func (m *MemStore) FinishObjectBucketObjectLock(_ context.Context, bucket, token string, c api.ObjectBucketObjectLockConfiguration) (ObjectBucketObjectLock, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.mutateObjectBucketObjectLockLocked(bucket, func(j ObjectBucketObjectLock, v ObjectBucketVersioning) (ObjectBucketObjectLock, error) {
		pending, unsafe, multipart, _ := m.capacityReadinessLocked(bucket)
		if objectLockNeedsDrain(j) && (pending > 0 || unsafe || multipart || m.activeCapacityLocked(bucket)) {
			return j, ErrConflict
		}
		return finishObjectBucketObjectLock(j, token, c, objectLockVersioningReady(v), m.clock())
	})
}

func (m *MemStore) RetryObjectBucketObjectLock(_ context.Context, bucket, token, code string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, err := m.mutateObjectBucketObjectLockLocked(bucket, func(j ObjectBucketObjectLock, _ ObjectBucketVersioning) (ObjectBucketObjectLock, error) {
		return retryObjectBucketObjectLock(j, token, code, m.clock())
	})
	return err
}
