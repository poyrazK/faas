package state

import (
	"context"
	"sort"
	"time"
)

const appLogDrainAnalyticsBucket = time.Hour

func (m *MemStore) UpsertAppLogDrainHealth(_ context.Context, health AppLogDrainHealth) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.appLogDrains[health.DrainID]; !ok {
		return ErrNotFound
	}
	if health.Status == "" {
		health.Status = "unknown"
	}
	if health.UpdatedAt.IsZero() {
		health.UpdatedAt = time.Now().UTC()
	}
	m.appLogDrainHealth[health.DrainID] = health
	sample := appLogDrainDeliveryAnalyticsFromHealth(health)
	sample.BucketStart = health.UpdatedAt.UTC().Truncate(appLogDrainAnalyticsBucket)
	sample.SampledAt = health.UpdatedAt.UTC()
	m.appLogDrainAnalytics[appLogDrainDeliveryAnalyticsKey(sample.DrainID, sample.BucketStart)] = sample
	return nil
}

func (m *MemStore) AppLogDrainHealthByDrainID(_ context.Context, drainID string) (AppLogDrainHealth, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	health, ok := m.appLogDrainHealth[drainID]
	if !ok {
		return AppLogDrainHealth{}, ErrNotFound
	}
	return health, nil
}

func (m *MemStore) ListAppLogDrainHealthForApp(_ context.Context, appID string) ([]AppLogDrainHealth, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]AppLogDrainHealth, 0)
	for drainID, health := range m.appLogDrainHealth {
		drain, ok := m.appLogDrains[drainID]
		if ok && drain.AppID == appID {
			out = append(out, health)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].DrainID < out[j].DrainID })
	return out, nil
}

func (m *MemStore) ListAppLogDrainDeliveryAnalytics(_ context.Context, drainID string, from, to time.Time) ([]AppLogDrainDeliveryAnalytics, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.appLogDrains[drainID]; !ok {
		return nil, ErrNotFound
	}
	out := make([]AppLogDrainDeliveryAnalytics, 0)
	for _, sample := range m.appLogDrainAnalytics {
		if sample.DrainID == drainID && !sample.BucketStart.Before(from) && !sample.BucketStart.After(to) {
			out = append(out, sample)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].BucketStart.Before(out[j].BucketStart) })
	return out, nil
}

func (m *MemStore) PruneAppLogDrainDeliveryAnalytics(_ context.Context, before time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for key, sample := range m.appLogDrainAnalytics {
		if sample.BucketStart.Before(before) {
			delete(m.appLogDrainAnalytics, key)
		}
	}
	return nil
}

func appLogDrainDeliveryAnalyticsKey(drainID string, bucketStart time.Time) string {
	return drainID + "\x00" + bucketStart.UTC().Format(time.RFC3339)
}

func appLogDrainDeliveryAnalyticsFromHealth(health AppLogDrainHealth) AppLogDrainDeliveryAnalytics {
	return AppLogDrainDeliveryAnalytics{
		DrainID: health.DrainID, DeliveredTotal: health.DeliveredTotal,
		FailedTotal: health.FailedTotal, DroppedTotal: health.DroppedTotal,
		RetriesTotal: health.RetriesTotal, DeadLetterTotal: health.DeadLetterTotal,
		DeliveryLatencyNanosTotal: health.DeliveryLatencyNanosTotal,
		DeliveryLatencySamples:    health.DeliveryLatencySamples,
		PendingRecords:            health.PendingRecords, PendingBytes: health.PendingBytes,
	}
}
