package state

import (
	"context"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

var _ ObjectVersionProtectionStore = (*MemStore)(nil)

func (m *MemStore) activeVersionProtectionLocked(bucket string) bool {
	for _, j := range m.objectVersionProtection {
		if j.BucketID == bucket && protectionActive(j) {
			return true
		}
	}
	return false
}

func (m *MemStore) BeginObjectVersionProtection(_ context.Context, j ObjectVersionProtection) (ObjectVersionProtection, error) {
	if !ValidObjectVersionProtectionIntent(j) {
		return ObjectVersionProtection{}, ErrConflict
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.objectBuckets[j.BucketID]
	if !ok || b.AccountID != j.AccountID || b.AppID != j.AppID {
		return ObjectVersionProtection{}, ErrNotFound
	}
	if old, ok := m.objectVersionProtection[j.ID]; ok {
		if old.AccountID != j.AccountID || old.BucketID != j.BucketID {
			return ObjectVersionProtection{}, ErrNotFound
		}
		if !sameProtectionIntent(old, j) {
			return old, ErrConflict
		}
		return cloneObjectVersionProtection(old), nil
	}
	lock := m.objectBucketObjectLock[b.ID]
	for _, old := range m.objectVersionProtection {
		if old.BucketID == b.ID && protectionActive(old) {
			if sameProtectionIntent(old, j) {
				return cloneObjectVersionProtection(old), nil
			}
			return ObjectVersionProtection{}, ErrConflict
		}
	}
	v := m.objectBucketVersioning[b.ID]
	encryption := m.objectBucketEncryption[b.ID]
	if encryption.State != "" && encryption.State != "ready" {
		return ObjectVersionProtection{}, ErrConflict
	}
	pending, unsafe, multipart, _ := m.capacityReadinessLocked(b.ID)
	if b.State != "ready" || pending > 0 || unsafe || multipart || m.objectCapacityFencedLocked(b.ID) || !lock.NativeEnabledObserved || !lock.ObservedKnown || lock.ObservedConfiguration == nil || !lock.ObservedConfiguration.Enabled || !objectLockVersioningReady(v) {
		return ObjectVersionProtection{}, ErrConflict
	}
	native := "null"
	if j.VersionID != "null" {
		identity, ok := m.objectVersionReferenceIDs[j.VersionID]
		ref := m.objectVersionReferences[identity]
		if !ok || identity != versionReferenceIdentity(b.ID, ref) || ref.Key != j.Key || ref.DeleteMarker || ref.ProviderVersionID == "null" {
			return ObjectVersionProtection{}, ErrNotFound
		}
		native = ref.ProviderVersionID
	}
	j = newProtectionIntent(j, native, m.clock())
	if m.objectVersionProtection == nil {
		m.objectVersionProtection = map[string]ObjectVersionProtection{}
	}
	m.objectVersionProtection[j.ID] = cloneObjectVersionProtection(j)
	return cloneObjectVersionProtection(j), nil
}
func (m *MemStore) GetObjectVersionProtection(_ context.Context, account, bucket, id string) (ObjectVersionProtection, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.objectVersionProtection[id]
	b := m.objectBuckets[bucket]
	if !ok || j.AccountID != account || j.BucketID != bucket || b.AccountID != account || b.State == "deleted" {
		return ObjectVersionProtection{}, ErrNotFound
	}
	return cloneObjectVersionProtection(j), nil
}
func (m *MemStore) DueObjectVersionProtection(_ context.Context, limit int32) ([]ObjectVersionProtection, error) {
	if limit < 1 || limit > api.ObjectVersionProtectionBatch {
		return nil, ErrConflict
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []ObjectVersionProtection{}
	for _, j := range m.objectVersionProtection {
		if protectionActive(j) && !j.RetryAt.After(m.clock()) && !j.LeaseUntil.After(m.clock()) {
			out = append(out, cloneObjectVersionProtection(j))
		}
	}
	sort.Slice(out, func(i, k int) bool {
		return out[i].RetryAt.Before(out[k].RetryAt) || out[i].RetryAt.Equal(out[k].RetryAt) && out[i].ID < out[k].ID
	})
	return out[:min(len(out), int(limit))], nil
}
func (m *MemStore) mutateProtection(id string, fn func(ObjectVersionProtection, time.Time) (ObjectVersionProtection, error)) (ObjectVersionProtection, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.objectVersionProtection[id]
	if !ok {
		return j, ErrNotFound
	}
	if m.objectBuckets[j.BucketID].State != "ready" {
		return j, ErrConflict
	}
	j, err := fn(cloneObjectVersionProtection(j), m.clock())
	if err == nil {
		m.objectVersionProtection[id] = cloneObjectVersionProtection(j)
	}
	return cloneObjectVersionProtection(j), err
}
func (m *MemStore) ClaimObjectVersionProtection(_ context.Context, id, token string) (ObjectVersionProtection, error) {
	return m.mutateProtection(id, func(j ObjectVersionProtection, now time.Time) (ObjectVersionProtection, error) {
		return claimProtection(j, token, now)
	})
}
func (m *MemStore) DispatchObjectVersionProtection(_ context.Context, id, token string) (ObjectVersionProtection, error) {
	return m.mutateProtection(id, func(j ObjectVersionProtection, now time.Time) (ObjectVersionProtection, error) {
		if !validProtectionLease(j, token, now) || j.Dispatched {
			return j, ErrConflict
		}
		j.Dispatched = true
		j.UpdatedAt = now
		return j, nil
	})
}
func (m *MemStore) FinishObjectVersionProtection(_ context.Context, id, token, status, code string) (ObjectVersionProtection, error) {
	return m.mutateProtection(id, func(j ObjectVersionProtection, now time.Time) (ObjectVersionProtection, error) {
		return finishProtection(j, token, status, code, now)
	})
}
func (m *MemStore) RetryObjectVersionProtection(_ context.Context, id, token, code string) error {
	_, err := m.mutateProtection(id, func(j ObjectVersionProtection, now time.Time) (ObjectVersionProtection, error) {
		return retryProtection(j, token, code, now)
	})
	return err
}
