package state

import (
	"context"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

var _ ObjectLifecycleStore = (*MemStore)(nil)

func (m *MemStore) ownedLifecyclePolicyLocked(account, app, bucket string) (ObjectLifecyclePolicy, error) {
	b, ok := m.objectBuckets[bucket]
	if !ok || b.AccountID != account || b.AppID != app {
		return ObjectLifecyclePolicy{}, ErrNotFound
	}
	if b.State != "ready" {
		return ObjectLifecyclePolicy{}, ErrConflict
	}
	p, ok := m.objectLifecyclePolicies[bucket]
	if !ok {
		p = newLifecyclePolicy(b, m.clock())
	}
	return cloneLifecyclePolicy(p), nil
}
func (m *MemStore) activeLifecycleScanLocked(bucket string) (ObjectLifecycleScan, bool) {
	for _, j := range m.objectLifecycleScans {
		if j.BucketID == bucket && j.State == "scanning" {
			return cloneLifecycleScan(j), true
		}
	}
	return ObjectLifecycleScan{}, false
}
func (m *MemStore) SetObjectBucketLifecycle(_ context.Context, account, app, bucket string, rules []api.ObjectLifecycleRule) (ObjectLifecyclePolicy, error) {
	normal, err := api.NormalizeObjectLifecycleRules(rules)
	if err != nil {
		return ObjectLifecyclePolicy{}, ErrConflict
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	p, err := m.ownedLifecyclePolicyLocked(account, app, bucket)
	if err != nil || lifecycleRulesEqual(p.Rules, normal) {
		return p, err
	}
	now := m.clock()
	if p.Revision >= api.MaxObjectStoragePolicyValue {
		return p, ErrConflict
	}
	if old, ok := m.activeLifecycleScanLocked(bucket); ok {
		if old.LeaseUntil.After(now) {
			return p, ErrConflict
		}
		old.State, old.Token, old.LeaseUntil, old.UpdatedAt, old.FinishedAt = "cancelled", "", time.Time{}, now, &now
		m.objectLifecycleScans[old.ID] = old
	}
	p.Revision++
	p.Rules, p.NextScanAt, p.UpdatedAt = normal, now, now
	if m.objectLifecyclePolicies == nil {
		m.objectLifecyclePolicies = map[string]ObjectLifecyclePolicy{}
	}
	m.objectLifecyclePolicies[bucket] = cloneLifecyclePolicy(p)
	return cloneLifecyclePolicy(p), nil
}
func (m *MemStore) GetObjectBucketLifecycle(_ context.Context, account, app, bucket string) (ObjectLifecyclePolicy, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.ownedLifecyclePolicyLocked(account, app, bucket)
}
func (m *MemStore) DueObjectLifecyclePolicies(_ context.Context, limit int32) ([]ObjectLifecyclePolicy, error) {
	if limit < 1 || limit > api.ObjectLifecycleBatch {
		return nil, ErrConflict
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []ObjectLifecyclePolicy{}
	for _, p := range m.objectLifecyclePolicies {
		b := m.objectBuckets[p.BucketID]
		if b.State != "ready" || !lifecycleEnabled(p) {
			continue
		}
		if j, active := m.activeLifecycleScanLocked(b.ID); active {
			if j.RetryAt.After(m.clock()) || j.LeaseUntil.After(m.clock()) {
				continue
			}
		} else if p.NextScanAt.After(m.clock()) {
			continue
		}
		out = append(out, cloneLifecyclePolicy(p))
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].NextScanAt.Before(out[j].NextScanAt) || out[i].NextScanAt.Equal(out[j].NextScanAt) && out[i].BucketID < out[j].BucketID
	})
	if len(out) > int(limit) {
		out = out[:limit]
	}
	return out, nil
}
func (m *MemStore) StartObjectLifecycleScan(_ context.Context, account, app, bucket string) (ObjectLifecycleScan, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, err := m.ownedLifecyclePolicyLocked(account, app, bucket)
	if err != nil {
		return ObjectLifecycleScan{}, err
	}
	if j, active := m.activeLifecycleScanLocked(bucket); active {
		return j, nil
	}
	if !lifecycleEnabled(p) || p.NextScanAt.After(m.clock()) {
		return ObjectLifecycleScan{}, ErrConflict
	}
	j := newLifecycleScan(p, m.clock())
	if m.objectLifecycleScans == nil {
		m.objectLifecycleScans = map[string]ObjectLifecycleScan{}
	}
	m.objectLifecycleScans[j.ID] = cloneLifecycleScan(j)
	return cloneLifecycleScan(j), nil
}
func (m *MemStore) GetObjectLifecycleScan(_ context.Context, account, bucket, id string) (ObjectLifecycleScan, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.objectLifecycleScans[id]
	b := m.objectBuckets[bucket]
	if !ok || j.AccountID != account || j.BucketID != bucket || b.AccountID != account {
		return ObjectLifecycleScan{}, ErrNotFound
	}
	return cloneLifecycleScan(j), nil
}
func (m *MemStore) mutateLifecycleScanLocked(id string, fn func(ObjectLifecycleScan) (ObjectLifecycleScan, error)) (ObjectLifecycleScan, error) {
	j, ok := m.objectLifecycleScans[id]
	if !ok {
		return j, ErrNotFound
	}
	p, err := m.ownedLifecyclePolicyLocked(j.AccountID, j.AppID, j.BucketID)
	if err != nil || p.Revision != j.Revision {
		return j, ErrConflict
	}
	j, err = fn(cloneLifecycleScan(j))
	if err == nil {
		m.objectLifecycleScans[id] = cloneLifecycleScan(j)
	}
	return cloneLifecycleScan(j), err
}
func (m *MemStore) ClaimObjectLifecycleScan(_ context.Context, id, token string) (ObjectLifecycleScan, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.mutateLifecycleScanLocked(id, func(j ObjectLifecycleScan) (ObjectLifecycleScan, error) {
		return claimLifecycleScan(j, token, m.clock())
	})
}
func (m *MemStore) CheckpointObjectLifecycleScan(_ context.Context, id, token, key string, done bool) (ObjectLifecycleScan, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, err := m.mutateLifecycleScanLocked(id, func(j ObjectLifecycleScan) (ObjectLifecycleScan, error) {
		return checkpointLifecycleScan(j, token, key, done, m.clock())
	})
	if err == nil && done {
		p := m.objectLifecyclePolicies[j.BucketID]
		p.NextScanAt = m.clock().Add(api.ObjectLifecycleSweepInterval)
		m.objectLifecyclePolicies[j.BucketID] = p
	}
	return j, err
}
func (m *MemStore) RetryObjectLifecycleScan(_ context.Context, id, token string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, err := m.mutateLifecycleScanLocked(id, func(j ObjectLifecycleScan) (ObjectLifecycleScan, error) {
		if !validLifecycleScanLease(j, token, m.clock()) {
			return j, ErrConflict
		}
		j.Token, j.LeaseUntil, j.RetryAt, j.UpdatedAt = "", time.Time{}, m.clock().Add(api.ObjectLifecycleRetry), m.clock()
		return j, nil
	})
	return err
}
