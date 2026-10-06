package state

import (
	"context"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

var _ ObjectCapacityStore = (*MemStore)(nil)

func (m *MemStore) objectCapacityFencedLocked(bucket string) bool {
	if objectLockActive(m.objectBucketObjectLock[bucket]) {
		return true
	}
	if m.activeDeletionLocked(bucket) {
		return true
	}
	if j, ok := m.objectBucketVersioning[bucket]; ok && versioningActive(j) {
		return true
	}
	for _, j := range m.objectCapacityJobs {
		if j.BucketID == bucket && objectCapacityActive(j.State) {
			return true
		}
	}
	return false
}

// A pending receipt owns its key until its result and accounting settle.
// Different keys remain independent. A fixed multipart reservation may reuse
// its own admission when preparing completion.
func (m *MemStore) objectWriteKeyFencedLocked(bucket, key, own string) bool {
	hash := objectKeyHash(key)
	for id, w := range m.objectWriteAdmissions {
		if id != own && w.BucketID == bucket && w.KeyHash == hash && !w.Settled &&
			(w.MultipartID == "" || objectMultipartLive(m.objectMultipartUploads[w.MultipartID].State)) {
			return true
		}
	}
	return false
}
func (m *MemStore) trackObjectGrantLocked(bucket, hash, token string, newGrant bool) {
	if m.objectTrackedGrants == nil {
		m.objectTrackedGrants = map[string]map[string]bool{}
	}
	if m.objectTrackedGrants[bucket] == nil {
		m.objectTrackedGrants[bucket] = map[string]bool{}
	}
	m.objectTrackedGrants[bucket][hash] = token != "" && (newGrant || m.objectTrackedGrants[bucket][hash])
}
func (m *MemStore) BeginObjectWrite(_ context.Context, account, bucket, token, key string, size int64, p api.ObjectStoragePolicy) error {
	if _, err := uuid.Parse(token); err != nil {
		return ErrConflict
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.objectUsage[bucket].InventoryScope == ObjectInventoryAllVersions || m.objectBucketDefaultRequiresTrackingLocked(bucket) {
		return ErrConflict
	}
	if _, exists := m.objectWriteAdmissions[token]; exists {
		return ErrConflict
	}
	if err := m.admitObjectURLLocked(account, bucket, key, size, true, p, token); err != nil {
		return err
	}
	if m.objectWriteAdmissions == nil {
		m.objectWriteAdmissions = map[string]objectWriteAdmission{}
	}
	m.objectWriteAdmissions[token] = objectWriteAdmission{BucketID: bucket, KeyHash: objectKeyHash(key)}
	return nil
}
func (m *MemStore) SettleObjectWrite(_ context.Context, account, bucket, token string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	w, ok := m.objectWriteAdmissions[token]
	if !ok || w.BucketID != bucket || m.objectBuckets[bucket].AccountID != account || (w.MultipartID != "" || w.Route) {
		return ErrNotFound
	}
	w.Settled = true
	m.objectWriteAdmissions[token] = w
	return nil
}
func (m *MemStore) capacityReadinessLocked(bucket string) (pending int64, unsafe, multipart, versions bool) {
	if m.activeDeletionLocked(bucket) {
		pending++
	}
	for _, c := range m.objectUploadCompletions {
		versions = versions || c.BucketID == bucket && c.RecoveryVersionsObserved
	}
	versions = versions || m.objectUsage[bucket].InventoryScope == ObjectInventoryAllVersions || m.versionAccountingStatusLocked(bucket).VersionsObserved
	for hash := range m.objectGrants[bucket] {
		if !m.objectTrackedGrants[bucket][hash] {
			unsafe = true
		}
	}
	for _, w := range m.objectWriteAdmissions {
		if w.BucketID == bucket && !w.Settled {
			if w.MultipartID == "" || objectMultipartLive(m.objectMultipartUploads[w.MultipartID].State) {
				pending++
			}
		}
	}
	for id, u := range m.objectMultipartUploads {
		if u.BucketID == bucket && (objectMultipartLive(u.State) || u.State != ObjectMultipartCompleted && len(m.objectMultipartPartGrants[id]) > 0) {
			multipart = true
		}
	}
	return
}
func (m *MemStore) RequestObjectCapacityReconciliation(_ context.Context, account, app, bucket string) (ObjectCapacityReconciliation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.objectBuckets[bucket]
	if !ok || b.AccountID != account || b.AppID != app {
		return ObjectCapacityReconciliation{}, ErrNotFound
	}
	if m.activeDeletionLocked(bucket) {
		return ObjectCapacityReconciliation{}, ErrConflict
	}
	if j, exists := m.objectBucketVersioning[bucket]; exists && versioningActive(j) {
		return ObjectCapacityReconciliation{}, ErrConflict
	}
	if b.State != "ready" {
		return ObjectCapacityReconciliation{}, ErrConflict
	}
	for _, j := range m.objectCapacityJobs {
		if j.BucketID == bucket && objectCapacityActive(j.State) {
			return cloneObjectCapacityJob(j), nil
		}
	}
	for _, u := range m.objectMultipartUploads {
		if u.BucketID == bucket && objectMultipartLive(u.State) {
			return ObjectCapacityReconciliation{}, ErrConflict
		}
	}
	now := m.clock().UTC()
	bytes, keys := objectCapacityTotals(m.objectUsageLocked(account, now), bucket)
	j := ObjectCapacityReconciliation{ObjectCapacityReconciliation: api.ObjectCapacityReconciliation{ID: uuid.NewString(), BucketID: bucket, State: "waiting", InventoryScope: ObjectInventoryCurrent, BeforeBytes: bytes, BeforeKeys: keys, AfterBytes: bytes, AfterKeys: keys, CreatedAt: now, UpdatedAt: now}, AccountID: account, AppID: app, RetryAt: now, DeadlineAt: now.Add(api.ObjectCapacityReconciliationTimeout)}
	if m.objectCapacityJobs == nil {
		m.objectCapacityJobs = map[string]ObjectCapacityReconciliation{}
	}
	m.objectCapacityJobs[j.ID] = j
	return cloneObjectCapacityJob(j), nil
}
func cloneObjectCapacityJob(j ObjectCapacityReconciliation) ObjectCapacityReconciliation {
	// Progress indexes belong to the store; workers need only the page cursor.
	j.InventoryEntries = nil
	j.InventoryCursors = nil
	if j.FinishedAt != nil {
		t := *j.FinishedAt
		j.FinishedAt = &t
	}
	return j
}
func (m *MemStore) GetObjectCapacityReconciliation(_ context.Context, account, bucket, id string) (ObjectCapacityReconciliation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.objectCapacityJobs[id]
	if !ok || j.AccountID != account || j.BucketID != bucket {
		return j, ErrNotFound
	}
	return cloneObjectCapacityJob(j), nil
}
func (m *MemStore) CancelObjectCapacityReconciliation(_ context.Context, account, bucket, id string) (ObjectCapacityReconciliation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.objectCapacityJobs[id]
	if !ok || j.AccountID != account || j.BucketID != bucket {
		return j, ErrNotFound
	}
	if objectCapacityActive(j.State) {
		now := m.clock().UTC()
		j.InventoryCursor = ""
		j.InventoryEntries = nil
		j.InventoryCursors = nil
		j.State = "cancelled"
		j.Token = ""
		j.LeaseUntil = time.Time{}
		j.UpdatedAt = now
		j.FinishedAt = &now
		m.objectCapacityJobs[id] = j
	}
	return cloneObjectCapacityJob(j), nil
}
func (m *MemStore) DueObjectCapacityReconciliations(_ context.Context, limit int32) ([]ObjectCapacityReconciliation, error) {
	if limit < 1 || limit > api.ObjectCapacityReconciliationBatch {
		return nil, ErrConflict
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.clock()
	out := []ObjectCapacityReconciliation{}
	for _, j := range m.objectCapacityJobs {
		if objectCapacityActive(j.State) && !j.RetryAt.After(now) && !j.LeaseUntil.After(now) {
			out = append(out, cloneObjectCapacityJob(j))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out[:min(int(limit), len(out))], nil
}
func (m *MemStore) ClaimObjectCapacityReconciliation(_ context.Context, id, token string) (ObjectCapacityReconciliation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.objectCapacityJobs[id]
	now := m.clock().UTC()
	if !ok {
		return j, ErrNotFound
	}
	if token == "" || !objectCapacityActive(j.State) || j.LeaseUntil.After(now) || j.RetryAt.After(now) {
		return j, ErrConflict
	}
	pending, unsafe, multipart, versions := m.capacityReadinessLocked(j.BucketID)
	if v, exists := m.objectBucketVersioning[j.BucketID]; exists && versioningActive(v) && (v.State != "inventory" || v.CapacityJobID != id) {
		pending = max(pending, 1)
	}
	j.BeforeBytes, j.BeforeKeys = objectCapacityTotals(m.objectUsageLocked(j.AccountID, now), j.BucketID)
	j.AfterBytes, j.AfterKeys = j.BeforeBytes, j.BeforeKeys
	j = prepareObjectCapacityClaim(j, token, pending, unsafe, multipart, versions, now)
	if !objectCapacityActive(j.State) {
		j.InventoryCursor = ""
		j.InventoryEntries = nil
		j.InventoryCursors = nil
	}
	m.objectCapacityJobs[id] = j
	return cloneObjectCapacityJob(j), nil
}
func (m *MemStore) FinishObjectCapacityReconciliation(_ context.Context, id, token string, bytes, keys int64) (ObjectCapacityReconciliation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.objectCapacityJobs[id]
	now := m.clock().UTC()
	if !ok {
		return j, ErrNotFound
	}
	if !validObjectCapacityFinish(j, token, bytes, keys, now) {
		return j, ErrConflict
	}
	if v, exists := m.objectBucketVersioning[j.BucketID]; exists && versioningActive(v) && (v.State != "inventory" || v.CapacityJobID != id) {
		return cloneObjectCapacityJob(j), ErrConflict
	}
	pending, unsafe, multipart, versions := m.capacityReadinessLocked(j.BucketID)
	if pending > 0 || unsafe || multipart || versions || m.objectBuckets[j.BucketID].State != "ready" {
		return j, ErrConflict
	}
	u := m.objectUsage[j.BucketID]
	u.BaselineBytes, u.BaselineKeys = bytes, keys
	u.ObservedBytes, u.ObservedKeys = bytes, keys
	u.GrantedBytes, u.GrantedKeys = 0, 0
	u.ObservedAt, u.AttemptAt = now, now
	u.Token = ""
	u.LeaseUntil = time.Time{}
	if m.objectUsage == nil {
		m.objectUsage = map[string]ObjectBucketUsage{}
	}
	m.objectUsage[j.BucketID] = u
	delete(m.objectGrants, j.BucketID)
	delete(m.objectTrackedGrants, j.BucketID)
	for k, w := range m.objectWriteAdmissions {
		if w.BucketID == j.BucketID {
			delete(m.objectWriteAdmissions, k)
		}
	}
	j = completeObjectCapacityJob(j, bytes, keys, now)
	m.objectCapacityJobs[id] = j
	return cloneObjectCapacityJob(j), nil
}
func validObjectCapacityFinish(j ObjectCapacityReconciliation, token string, bytes, keys int64, now time.Time) bool {
	return j.State == "scanning" && token != "" && j.Token == token && j.LeaseUntil.After(now) && bytes >= 0 && keys >= 0 && bytes <= api.MaxObjectStoragePolicyValue && keys <= api.MaxObjectStoragePolicyValue
}
func (m *MemStore) RetryObjectCapacityReconciliation(_ context.Context, id, token string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.objectCapacityJobs[id]
	now := m.clock().UTC()
	if !ok {
		return ErrNotFound
	}
	if !validObjectCapacityFinish(j, token, 0, 0, now) {
		return ErrConflict
	}
	j.State = "waiting"
	j.Token = ""
	j.LeaseUntil = time.Time{}
	j.UpdatedAt = now
	j.RetryAt = now.Add(api.ObjectCapacityReconciliationRetry)
	j.LastErrorCode = "inventory_failed"
	m.objectCapacityJobs[id] = j
	return nil
}
