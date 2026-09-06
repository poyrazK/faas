package state

import (
	"context"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

var _ ObjectStorageAccountingStore = (*MemStore)(nil)
var _ ObjectStorageBillingStore = (*MemStore)(nil)

func (m *MemStore) ObjectUsage(_ context.Context, account string, now time.Time) (ObjectUsageSnapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.objectUsageLocked(account, now), nil
}

func (m *MemStore) ObjectUsageForPeriod(_ context.Context, account string, periodStart time.Time) (ObjectUsageSnapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.objectUsageLockedForPeriod(account, periodStart), nil
}

func (m *MemStore) objectUsageLocked(account string, now time.Time) ObjectUsageSnapshot {
	return m.objectUsageLockedForPeriod(account, ObjectStoragePeriod(now))
}

func (m *MemStore) objectUsageLockedForPeriod(account string, periodStart time.Time) ObjectUsageSnapshot {
	periodStart = ObjectStoragePeriod(periodStart)
	out := ObjectUsageSnapshot{Authorizations: m.objectAuthorizations[account+periodStart.String()]}
	for _, b := range m.objectBuckets {
		if b.AccountID == account {
			u := m.objectUsage[b.ID]
			u.Bucket = b
			out.Buckets = append(out.Buckets, u)
		}
	}
	latest := map[string]api.ObjectStorageUsageReport{}
	for _, r := range m.objectReports {
		if r.AccountID == account && r.PeriodStart.Equal(periodStart) && r.ObservedAt.After(latest[r.BackendID].ObservedAt) {
			latest[r.BackendID] = r
		}
	}
	for _, r := range latest {
		out.Reports = append(out.Reports, r)
	}
	return out
}

func (m *MemStore) GetObjectStorageBillingPeriod(_ context.Context, account string, periodStart time.Time) (ObjectStorageBillingRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.objectStorageBilling == nil {
		return ObjectStorageBillingRecord{}, ErrNotFound
	}
	record, ok := m.objectStorageBilling[objectBillingKey(account, periodStart)]
	if !ok {
		return ObjectStorageBillingRecord{}, ErrNotFound
	}
	return record, nil
}

func (m *MemStore) RecordObjectStorageBillingPeriod(_ context.Context, record ObjectStorageBillingRecord) (ObjectStorageBillingRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	record = normalizeObjectStorageBillingRecord(record)
	if err := validateObjectStorageBillingRecord(record); err != nil {
		return ObjectStorageBillingRecord{}, err
	}
	if m.objectStorageBilling == nil {
		m.objectStorageBilling = map[string]ObjectStorageBillingRecord{}
	}
	key := objectBillingKey(record.AccountID, record.PeriodStart)
	if existing, ok := m.objectStorageBilling[key]; ok {
		if sameObjectStorageBillingRecord(existing, record) {
			return existing, nil
		}
		return ObjectStorageBillingRecord{}, ErrObjectBillingConflict
	}
	if record.ID == "" {
		record.ID = uuid.NewString()
	}
	m.objectStorageBilling[key] = record
	return record, nil
}

func objectBillingKey(account string, periodStart time.Time) string {
	return account + "\x00" + ObjectStoragePeriod(periodStart).Format(time.RFC3339)
}

func (m *MemStore) AdmitObjectURL(_ context.Context, account, bucket, key string, size int64, put bool, p api.ObjectStoragePolicy) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC()
	s := m.objectUsageLocked(account, now)
	hash := objectKeyHash(key)
	old, exists := m.objectGrants[bucket][hash]
	delta, keys, err := checkObjectAdmission(s, bucket, size, old, exists, put, p, now)
	if err != nil {
		return err
	}
	if put {
		if m.objectGrants == nil {
			m.objectGrants = map[string]map[string]int64{}
		}
		if m.objectGrants[bucket] == nil {
			m.objectGrants[bucket] = map[string]int64{}
		}
		m.objectGrants[bucket][hash] = max(old, size)
		u := m.objectUsage[bucket]
		u.GrantedBytes += delta
		u.GrantedKeys += keys
		m.objectUsage[bucket] = u
	}
	if m.objectAuthorizations == nil {
		m.objectAuthorizations = map[string]int64{}
	}
	m.objectAuthorizations[account+ObjectStoragePeriod(now).String()]++
	return nil
}

func (m *MemStore) RecordObjectUsageReport(_ context.Context, r api.ObjectStorageUsageReport) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r = normalizeObjectReport(r)
	if !validObjectReport(r, time.Now()) {
		return ErrConflict
	}
	for _, old := range m.objectReports {
		if old.AccountID == r.AccountID && old.BackendID == r.BackendID && old.PeriodStart.Equal(r.PeriodStart) && old.ObservedAt.Equal(r.ObservedAt) {
			if sameObjectReport(old, r) {
				return nil
			}
			return ErrConflict
		}
	}
	if err := checkObjectReport(m.objectUsageLocked(r.AccountID, r.ObservedAt), r); err != nil {
		return err
	}
	m.objectReports = append(m.objectReports, r)
	return nil
}

func (m *MemStore) DueObjectInventories(_ context.Context, limit int32) ([]ObjectBucket, error) {
	if limit < 1 || limit > 100 {
		return nil, ErrConflict
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	out := []ObjectBucket{}
	for _, b := range m.objectBuckets {
		u := m.objectUsage[b.ID]
		if b.State == "ready" && (u.AttemptAt.IsZero() || now.Sub(u.AttemptAt) > 5*time.Minute) && !u.LeaseUntil.After(now) {
			out = append(out, b)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := m.objectUsage[out[i].ID].AttemptAt, m.objectUsage[out[j].ID].AttemptAt
		return a.Before(b) || a.Equal(b) && out[i].ID < out[j].ID
	})
	if len(out) > int(limit) {
		out = out[:limit]
	}
	return out, nil
}

func (m *MemStore) ClaimObjectInventory(_ context.Context, bucket, token string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	u := m.objectUsage[bucket]
	if token == "" || m.objectBuckets[bucket].State != "ready" || u.LeaseUntil.After(time.Now()) {
		return ErrConflict
	}
	u.Token = token
	u.AttemptAt = time.Now().UTC()
	u.LeaseUntil = u.AttemptAt.Add(2 * time.Minute)
	if m.objectUsage == nil {
		m.objectUsage = map[string]ObjectBucketUsage{}
	}
	m.objectUsage[bucket] = u
	return nil
}

func (m *MemStore) FinishObjectInventory(_ context.Context, bucket, token string, bytes, objects int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	u := m.objectUsage[bucket]
	if token == "" || u.Token != token || !u.LeaseUntil.After(time.Now()) || m.objectBuckets[bucket].State != "ready" || bytes < 0 || objects < 0 || bytes > api.MaxObjectStoragePolicyValue || objects > api.MaxObjectStoragePolicyValue {
		return ErrConflict
	}
	if u.ObservedAt.IsZero() {
		u.BaselineBytes, u.BaselineKeys = bytes, objects
	}
	u.ObservedBytes, u.ObservedKeys, u.ObservedAt = bytes, objects, u.AttemptAt
	u.Token, u.LeaseUntil = "", time.Time{}
	m.objectUsage[bucket] = u
	return nil
}
