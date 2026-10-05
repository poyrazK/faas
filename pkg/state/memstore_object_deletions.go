package state

import (
	"context"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

var _ ObjectDeletionStore = (*MemStore)(nil)
var _ ObjectDeletionActivityStore = (*MemStore)(nil)

func (m *MemStore) HasActiveObjectDeletion(_ context.Context, account, app, bucket string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.objectBuckets[bucket]
	if !ok || b.AccountID != account || b.AppID != app {
		return false, ErrNotFound
	}
	return m.activeDeletionLocked(bucket), nil
}

func (m *MemStore) activeDeletionLocked(bucket string) bool {
	for _, j := range m.objectDeletions {
		if j.BucketID == bucket && deletionActive(j) {
			return true
		}
	}
	return false
}
func (m *MemStore) BeginObjectDeletion(_ context.Context, j ObjectDeletion, policy api.ObjectStoragePolicy) (ObjectDeletion, bool, error) {
	if !validDeletionIdentity(j) {
		return ObjectDeletion{}, false, ErrConflict
	}
	j = newDeletionIntent(j)
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.objectBuckets[j.BucketID]
	if !ok || b.AccountID != j.AccountID || b.AppID != j.AppID {
		return ObjectDeletion{}, false, ErrNotFound
	}
	if old, ok := m.objectDeletions[j.ID]; ok {
		if old.AccountID != j.AccountID || old.BucketID != j.BucketID {
			return ObjectDeletion{}, false, ErrNotFound
		}
		if old.Key != j.Key || old.Selector != j.Selector || !sameLifecycleDeletionBinding(old.Lifecycle, j.Lifecycle) {
			return old, false, ErrConflict
		}
		return cloneDeletion(old), false, nil
	}
	if immutableDeletion(j) {
		identity, exists := m.objectVersionReferenceIDs[j.Selector]
		v := m.objectVersionReferences[identity]
		if !exists || v.Key != j.Key || v.ProviderVersionID == "null" || identity != versionReferenceIdentity(b.ID, v) {
			return ObjectDeletion{}, false, ErrNotFound
		}
		j.TargetProviderVersionID = v.ProviderVersionID
	}
	if err := m.validateLifecycleDeletionLocked(j); err != nil {
		return ObjectDeletion{}, false, err
	}
	pending, unsafe, multipart, versions := m.capacityReadinessLocked(b.ID)
	v := m.objectBucketVersioning[b.ID]
	if b.State != "ready" || m.objectCapacityFencedLocked(b.ID) || pending > 0 || multipart || !immutableDeletion(j) && unsafe && (versions || v.ObservedStatus != "") {
		return ObjectDeletion{}, false, ErrConflict
	}
	if !immutableDeletion(j) && versions && (m.objectUsage[b.ID].InventoryScope != ObjectInventoryAllVersions || v.ObservedStatus == "") {
		return ObjectDeletion{}, false, ErrConflict
	}
	if !immutableDeletion(j) {
		j.ProviderStatus = v.ObservedStatus
	}
	if j.Selector == "" && j.ProviderStatus != "" {
		j.ReservedBytes = int64(len(j.Key))
		if _, _, err := checkObjectAdmission(m.objectUsageLocked(j.AccountID, m.clock()), b.ID, j.ReservedBytes, 0, false, true, policy, m.clock()); err != nil {
			return ObjectDeletion{}, false, err
		}
		u := m.objectUsage[b.ID]
		u.GrantedBytes += j.ReservedBytes
		u.GrantedKeys++
		m.objectUsage[b.ID] = u
	}
	now := m.clock()
	j.State = "prepared"
	j.LeaseUntil = now.Add(api.ObjectDeletionLease)
	j.RetryAt = now
	j.CreatedAt = now
	j.UpdatedAt = now
	if m.objectDeletions == nil {
		m.objectDeletions = map[string]ObjectDeletion{}
	}
	m.objectDeletions[j.ID] = cloneDeletion(j)
	return cloneDeletion(j), true, nil
}
func (m *MemStore) GetObjectDeletion(_ context.Context, account, bucket, id string) (ObjectDeletion, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.objectDeletions[id]
	if !ok || j.AccountID != account || j.BucketID != bucket {
		return ObjectDeletion{}, ErrNotFound
	}
	return cloneDeletion(j), nil
}
func (m *MemStore) DispatchObjectDeletion(_ context.Context, id, token, status string, baseline []string) (ObjectDeletion, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j := m.objectDeletions[id]
	if err := m.validateLifecycleDeletionLocked(j); err != nil {
		return cloneDeletion(j), err
	}
	if j.ProviderStatus != status {
		return cloneDeletion(j), ErrConflict
	}
	j, err := dispatchDeletion(j, token, status, baseline, m.clock())
	if err == nil {
		m.objectDeletions[id] = cloneDeletion(j)
	}
	return cloneDeletion(j), err
}
func (m *MemStore) FinishObjectDeletion(_ context.Context, result ObjectDeletion) (ObjectDeletion, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j := m.objectDeletions[result.ID]
	if result.ProviderVersionID != "" && !validVersionReferences([]ObjectVersionIdentity{{Key: j.Key, ProviderVersionID: result.ProviderVersionID, DeleteMarker: result.DeleteMarker}}) {
		return j, ErrConflict
	}
	// Validate the lease before generating an owned reference.
	if !validDeletionLease(j, result.Token, m.clock()) {
		return cloneDeletion(j), ErrConflict
	}
	var version ObjectVersionIdentity
	if result.ProviderVersionID != "" {
		version = m.prepareObjectVersionLocked(j.BucketID, ObjectVersionIdentity{Key: j.Key, ProviderVersionID: result.ProviderVersionID, DeleteMarker: result.DeleteMarker})
		result.VersionID = publicPreparedVersionID(version)
	}
	out, err := finishDeletion(j, result, m.clock())
	if err != nil {
		return cloneDeletion(j), err
	}
	if out.State == "completed" {
		typ, data := deletionEvent(out)
		if err = m.publishObjectEventLocked(out.AccountID, "delete:"+out.ID, typ, data, out.UpdatedAt); err != nil {
			return cloneDeletion(j), err
		}
	}
	if version.ProviderVersionID != "" {
		m.commitObjectVersionLocked(j.BucketID, version)
	}
	if out.State == "failed" && j.ReservedBytes > 0 {
		u := m.objectUsage[j.BucketID]
		u.GrantedBytes -= j.ReservedBytes
		u.GrantedKeys--
		m.objectUsage[j.BucketID] = u
	}
	m.objectDeletions[j.ID] = cloneDeletion(out)
	return cloneDeletion(out), nil
}
func (m *MemStore) DueObjectDeletions(_ context.Context, limit int32) ([]ObjectDeletion, error) {
	if limit < 1 || limit > api.ObjectDeletionBatch {
		return nil, ErrConflict
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []ObjectDeletion{}
	now := m.clock()
	for _, j := range m.objectDeletions {
		if deletionActive(j) && !j.RetryAt.After(now) && !j.LeaseUntil.After(now) {
			out = append(out, cloneDeletion(j))
		}
	}
	sort.Slice(out, func(i, k int) bool {
		if out[i].RetryAt.Equal(out[k].RetryAt) {
			return out[i].ID < out[k].ID
		}
		return out[i].RetryAt.Before(out[k].RetryAt)
	})
	return out[:min(len(out), int(limit))], nil
}
func (m *MemStore) ClaimObjectDeletion(_ context.Context, id, token string) (ObjectDeletion, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.objectDeletions[id]
	now := m.clock()
	if !ok {
		return j, ErrNotFound
	}
	if !deletionActive(j) || token == "" || len(token) > 128 || j.LeaseUntil.After(now) || j.RetryAt.After(now) {
		return cloneDeletion(j), ErrConflict
	}
	j.Token = token
	j.RecoveryClaimed = j.RecoveryClaimed || j.State == "dispatched"
	j.LeaseUntil = now.Add(api.ObjectDeletionLease)
	j.UpdatedAt = now
	m.objectDeletions[id] = cloneDeletion(j)
	return cloneDeletion(j), nil
}
func (m *MemStore) RetryObjectDeletion(_ context.Context, id, token, code string) error {
	if code != "provider_uncertain" && code != "configuration" {
		return ErrConflict
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	j := m.objectDeletions[id]
	now := m.clock()
	if !validDeletionLease(j, token, now) {
		return ErrConflict
	}
	j.Token = ""
	j.LeaseUntil = time.Time{}
	j.RetryAt = now.Add(api.ObjectDeletionRetry)
	j.UpdatedAt = now
	j.LastErrorCode = code
	m.objectDeletions[id] = cloneDeletion(j)
	return nil
}
