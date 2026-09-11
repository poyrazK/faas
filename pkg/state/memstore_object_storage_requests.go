package state

import (
	"context"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

var _ ObjectStorageProviderUsageStore = (*MemStore)(nil)

func objectProviderRequestKey(bucketID string, periodStart time.Time) string {
	return bucketID + "\x00" + ObjectStoragePeriod(periodStart).Format(time.RFC3339)
}

func (m *MemStore) RecordObjectStorageProviderRequest(_ context.Context, bucketID string, at time.Time) error {
	if bucketID == "" || at.IsZero() || at.After(time.Now().UTC().Add(time.Minute)) {
		return ErrConflict
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	bucket, ok := m.objectBuckets[bucketID]
	if !ok || bucket.State == "deleted" {
		return ErrNotFound
	}
	period := ObjectStoragePeriod(at)
	key := objectProviderRequestKey(bucketID, period)
	if m.objectProviderRequests == nil {
		m.objectProviderRequests = map[string]int64{}
	}
	if m.objectProviderRequests[key] >= 1<<60 {
		return ErrConflict
	}
	m.objectProviderRequests[key]++
	return nil
}

func (m *MemStore) RecordObjectStorageProviderEgress(_ context.Context, bucketID string, bytes int64, at time.Time) error {
	if bucketID == "" || bytes < 0 || bytes > api.MaxObjectStoragePolicyValue || at.IsZero() || at.After(time.Now().UTC().Add(time.Minute)) {
		return ErrConflict
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	bucket, ok := m.objectBuckets[bucketID]
	if !ok || bucket.State == "deleted" {
		return ErrNotFound
	}
	key := objectProviderRequestKey(bucketID, ObjectStoragePeriod(at)) + "\x00egress"
	if m.objectProviderRequests[key] > api.MaxObjectStoragePolicyValue-bytes {
		return ErrConflict
	}
	if m.objectProviderRequests == nil {
		m.objectProviderRequests = map[string]int64{}
	}
	m.objectProviderRequests[key] += bytes
	return nil
}

func (m *MemStore) ListObjectStorageProviderRequestMetrics(_ context.Context, backendID, fingerprint string, periodStart time.Time) ([]ObjectStorageProviderRequestMetric, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	periodStart = ObjectStoragePeriod(periodStart)
	out := make([]ObjectStorageProviderRequestMetric, 0)
	for id, bucket := range m.objectBuckets {
		if bucket.BackendID != backendID || bucket.BackendFingerprint != fingerprint {
			continue
		}
		out = append(out, ObjectStorageProviderRequestMetric{
			BucketID: id, AccountID: bucket.AccountID, BackendID: bucket.BackendID,
			BackendFingerprint: bucket.BackendFingerprint, PhysicalName: bucket.PhysicalName,
			PeriodStart: periodStart, RequestCount: m.objectProviderRequests[objectProviderRequestKey(id, periodStart)],
			EgressBytes: m.objectProviderRequests[objectProviderRequestKey(id, periodStart)+"\x00egress"],
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].PhysicalName == out[j].PhysicalName {
			return out[i].BucketID < out[j].BucketID
		}
		return out[i].PhysicalName < out[j].PhysicalName
	})
	return out, nil
}

func (m *MemStore) ListObjectStorageProviderBuckets(_ context.Context, backendID, fingerprint string) ([]ObjectBucket, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]ObjectBucket, 0)
	for _, bucket := range m.objectBuckets {
		if bucket.BackendID == backendID && bucket.BackendFingerprint == fingerprint {
			out = append(out, bucket)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].PhysicalName == out[j].PhysicalName {
			return out[i].ID < out[j].ID
		}
		return out[i].PhysicalName < out[j].PhysicalName
	})
	return out, nil
}
