package state

import (
	"context"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"sort"
)

var _ ObjectBucketVersioningStore = (*MemStore)(nil)

func (m *MemStore) saveObjectVersioningLocked(j ObjectBucketVersioning) {
	if m.objectBucketVersioning == nil {
		m.objectBucketVersioning = map[string]ObjectBucketVersioning{}
	}
	m.objectBucketVersioning[j.BucketID] = cloneObjectVersioning(j)
}
func (m *MemStore) ownedObjectVersioningLocked(account, app, bucket string) (ObjectBucketVersioning, error) {
	b, ok := m.objectBuckets[bucket]
	if !ok || b.AccountID != account || b.AppID != app {
		return ObjectBucketVersioning{}, ErrNotFound
	}
	if b.State != "ready" {
		return ObjectBucketVersioning{}, ErrConflict
	}
	j, ok := m.objectBucketVersioning[bucket]
	if !ok {
		j = newObjectVersioning(b, m.clock())
	}
	return cloneObjectVersioning(j), nil
}
func (m *MemStore) RequestObjectBucketVersioning(_ context.Context, account, app, bucket, status string) (ObjectBucketVersioning, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, err := m.ownedObjectVersioningLocked(account, app, bucket)
	if err != nil {
		return j, err
	}
	if lock := m.objectBucketObjectLock[bucket]; status == "Suspended" && (lock.EnabledRequired || objectLockActive(lock)) {
		return j, ErrConflict
	}
	_, unsafe, _, _ := m.capacityReadinessLocked(bucket)
	if unsafe || m.activeDeletionLocked(bucket) {
		return j, ErrConflict
	}
	if !versioningActive(j) && m.activeCapacityLocked(bucket) {
		return j, ErrConflict
	}
	j, err = requestObjectVersioning(j, status, m.clock())
	if err == nil {
		m.saveObjectVersioningLocked(j)
	}
	return cloneObjectVersioning(j), err
}
func (m *MemStore) ObserveObjectBucketVersioning(_ context.Context, account, app, bucket, status string) (ObjectBucketVersioning, error) {
	if status != "" && !ValidObjectBucketVersioningStatus(status) {
		return ObjectBucketVersioning{}, ErrConflict
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	j, err := m.ownedObjectVersioningLocked(account, app, bucket)
	if err != nil {
		return j, err
	}
	j = observeObjectVersioning(j, status, m.clock())
	m.saveObjectVersioningLocked(j)
	return cloneObjectVersioning(j), nil
}
func (m *MemStore) GetObjectBucketVersioning(_ context.Context, account, app, bucket string) (ObjectBucketVersioning, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.ownedObjectVersioningLocked(account, app, bucket)
}
func (m *MemStore) activeCapacityLocked(bucket string) bool {
	for _, c := range m.objectCapacityJobs {
		if c.BucketID == bucket && objectCapacityActive(c.State) {
			return true
		}
	}
	return false
}
func (m *MemStore) DueObjectBucketVersioning(_ context.Context, limit int32) ([]ObjectBucketVersioning, error) {
	if limit < 1 || limit > api.ObjectBucketVersioningBatch {
		return nil, ErrConflict
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.clock()
	out := []ObjectBucketVersioning{}
	for _, j := range m.objectBucketVersioning {
		if versioningActive(j) && !j.LeaseUntil.After(now) && !j.RetryAt.After(now) {
			out = append(out, cloneObjectVersioning(j))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].BucketID < out[j].BucketID })
	return out[:min(int(limit), len(out))], nil
}
func (m *MemStore) ClaimObjectBucketVersioning(_ context.Context, bucket, token string) (ObjectBucketVersioning, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.objectBucketVersioning[bucket]
	if !ok {
		return j, ErrNotFound
	}
	if m.objectBuckets[bucket].State != "ready" {
		return j, ErrConflict
	}
	pending, unsafe, multipart, _ := m.capacityReadinessLocked(bucket)
	if j.State == "inventory" {
		c := m.objectCapacityJobs[j.CapacityJobID]
		if c.State == "completed" && c.InventoryVerified {
			if j.PropagationUntil != nil && c.CreatedAt.Before(*j.PropagationUntil) {
				j.CapacityJobID = ""
				j.State = "propagating"
			} // A superseded configuration's inventory only drains its worker.
		} else if objectCapacityActive(c.State) {
			return cloneObjectVersioning(j), ErrConflict
		} else {
			j.CapacityJobID = ""
			j.State = "propagating"
			j.LastErrorCode = "inventory_failed"
		}
	}
	j, err := claimObjectVersioning(j, token, pending, unsafe, multipart, m.activeCapacityLocked(bucket), m.clock())
	if err == nil {
		m.saveObjectVersioningLocked(j)
	}
	return cloneObjectVersioning(j), err
}
func (m *MemStore) DispatchObjectBucketVersioning(_ context.Context, bucket, token string) (ObjectBucketVersioning, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j := m.objectBucketVersioning[bucket]
	now := m.clock()
	if !validVersioningLease(j, token, now) || j.State == "inventory" {
		return cloneObjectVersioning(j), ErrConflict
	}
	pending, unsafe, multipart, _ := m.capacityReadinessLocked(bucket)
	if pending > 0 || unsafe || multipart || m.activeCapacityLocked(bucket) {
		return cloneObjectVersioning(j), ErrConflict
	}
	j.Dispatched = true
	j.VersionsRequired = true
	t := now.Add(api.ObjectBucketVersioningPropagation)
	j.PropagationUntil = &t
	j.UpdatedAt = now
	m.saveObjectVersioningLocked(j)
	return cloneObjectVersioning(j), nil
}
func (m *MemStore) AdvanceObjectBucketVersioning(_ context.Context, bucket, token, status string) (ObjectBucketVersioning, error) {
	if status != "" && !ValidObjectBucketVersioningStatus(status) {
		return ObjectBucketVersioning{}, ErrConflict
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	j := m.objectBucketVersioning[bucket]
	now := m.clock()
	if !validVersioningLease(j, token, now) {
		return cloneObjectVersioning(j), ErrConflict
	}
	c := m.objectCapacityJobs[j.CapacityJobID]
	done := c.State == "completed" && c.InventoryVerified && c.InventoryScope == ObjectInventoryAllVersions
	j, inventory := advanceObjectVersioning(j, status, done, now)
	if inventory {
		if m.activeCapacityLocked(bucket) {
			return cloneObjectVersioning(j), ErrConflict
		}
		pending, unsafe, multipart, _ := m.capacityReadinessLocked(bucket)
		if pending > 0 || unsafe || multipart {
			return cloneObjectVersioning(j), ErrConflict
		}
		bytes, keys := objectCapacityTotals(m.objectUsageLocked(j.AccountID, now), bucket)
		c = ObjectCapacityReconciliation{ObjectCapacityReconciliation: api.ObjectCapacityReconciliation{ID: uuid.NewString(), BucketID: bucket, State: "waiting", InventoryScope: ObjectInventoryAllVersions, BeforeBytes: bytes, BeforeKeys: keys, AfterBytes: bytes, AfterKeys: keys, CreatedAt: now, UpdatedAt: now}, AccountID: j.AccountID, AppID: j.AppID, RetryAt: now, DeadlineAt: now.Add(api.ObjectCapacityReconciliationTimeout)}
		if m.objectCapacityJobs == nil {
			m.objectCapacityJobs = map[string]ObjectCapacityReconciliation{}
		}
		m.objectCapacityJobs[c.ID] = c
		j.CapacityJobID = c.ID
	}
	m.saveObjectVersioningLocked(j)
	return cloneObjectVersioning(j), nil
}
func (m *MemStore) RetryObjectBucketVersioning(_ context.Context, bucket, token string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	j := m.objectBucketVersioning[bucket]
	now := m.clock()
	if !validVersioningLease(j, token, now) {
		return ErrConflict
	}
	j = releaseObjectVersioning(j, now)
	j.LastErrorCode = "provider_failed"
	m.saveObjectVersioningLocked(j)
	return nil
}
