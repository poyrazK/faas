package state

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

var _ ObjectVersionInventoryStore = (*MemStore)(nil)

func (m *MemStore) versionAccountingStatusLocked(bucket string) ObjectVersionAccountingStatus {
	s := ObjectVersionAccountingStatus{Scope: m.objectUsage[bucket].InventoryScope}
	if s.Scope == "" {
		s.Scope = ObjectInventoryCurrent
	}
	for _, c := range m.objectUploadCompletions {
		s.VersionsObserved = s.VersionsObserved || c.BucketID == bucket && c.RecoveryVersionsObserved
	}
	for _, u := range m.objectMultipartUploads {
		s.VersionsObserved = s.VersionsObserved || u.BucketID == bucket && u.CompletionVersionsObserved
	}
	s.VersionsObserved = s.VersionsObserved || m.objectVersionObservations[bucket]
	for _, j := range m.objectCapacityJobs {
		s.NativeScanActive = s.NativeScanActive || j.BucketID == bucket && j.InventoryScope == ObjectInventoryAllVersions && objectCapacityActive(j.State)
	}
	return s
}

func (m *MemStore) ObjectVersionAccountingStatus(_ context.Context, account, bucket string) (ObjectVersionAccountingStatus, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.objectBuckets[bucket]
	if !ok || b.AccountID != account {
		return ObjectVersionAccountingStatus{}, ErrNotFound
	}
	return m.versionAccountingStatusLocked(bucket), nil
}

func (m *MemStore) StageObjectVersionInventoryPage(_ context.Context, id, token, next string, items []ObjectVersionInventoryRecord) (ObjectCapacityReconciliation, error) {
	if !validVersionInventoryPage(next, items) {
		return ObjectCapacityReconciliation{}, ErrConflict
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.objectCapacityJobs[id]
	if !ok {
		return cloneObjectCapacityJob(j), ErrNotFound
	}
	now := time.Now().UTC()
	if !validObjectCapacityFinish(j, token, 0, 0, now) || j.InventoryScope != ObjectInventoryAllVersions || j.ScannedPages >= api.ObjectStorageInventoryMaxPages || m.objectBuckets[j.BucketID].State != "ready" {
		return cloneObjectCapacityJob(j), ErrConflict
	}
	pending, unsafe, multipart, _ := m.capacityReadinessLocked(j.BucketID)
	if pending > 0 || unsafe || multipart {
		return cloneObjectCapacityJob(j), ErrConflict
	}
	if next != "" && j.InventoryCursors[objectKeyHash(next)] {
		return cloneObjectCapacityJob(j), ErrConflict
	}
	for _, item := range items {
		if j.InventoryEntries[item.Identity] {
			return cloneObjectCapacityJob(j), ErrConflict
		}
		j.ScannedBytes = boundedObjectAdd(j.ScannedBytes, item.Bytes)
	}
	j.ScannedPages++
	j.ScannedVersions += int64(len(items))
	if j.ScannedBytes > api.MaxObjectStoragePolicyValue || j.ScannedVersions > api.ObjectStorageInventoryMaxPages*api.ObjectVersionInventoryPageSize || next != "" && j.ScannedPages == api.ObjectStorageInventoryMaxPages {
		return cloneObjectCapacityJob(j), ErrConflict
	}
	// Mutate indexes only after every page check succeeds, so rejected pages
	// retain prior progress without copying the complete index per request.
	if j.InventoryEntries == nil {
		j.InventoryEntries = map[string]bool{}
	}
	for _, item := range items {
		j.InventoryEntries[item.Identity] = true
	}
	if next != "" {
		if j.InventoryCursors == nil {
			j.InventoryCursors = map[string]bool{}
		}
		j.InventoryCursors[objectKeyHash(next)] = true
		j.InventoryCursor = next
		j.State = "waiting"
		j.Token = ""
		j.LeaseUntil = time.Time{}
		j.RetryAt = now
		j.UpdatedAt = now
		m.objectCapacityJobs[id] = j
		return cloneObjectCapacityJob(j), nil
	}
	u := m.objectUsage[j.BucketID]
	u.InventoryScope = ObjectInventoryAllVersions
	u.BaselineBytes = j.ScannedBytes
	u.BaselineKeys = j.ScannedVersions
	u.ObservedBytes = j.ScannedBytes
	u.ObservedKeys = j.ScannedVersions
	u.GrantedBytes = 0
	u.GrantedKeys = 0
	u.ObservedAt = now
	u.AttemptAt = now
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
	j.InventoryCursor = ""
	j.InventoryVerified = true
	j.InventoryEntries = nil
	j.InventoryCursors = nil
	j = completeObjectCapacityJob(j, j.ScannedBytes, j.ScannedVersions, now)
	m.objectCapacityJobs[id] = j
	return cloneObjectCapacityJob(j), nil
}
