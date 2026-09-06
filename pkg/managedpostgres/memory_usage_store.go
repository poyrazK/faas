package managedpostgres

import (
	"context"
	"sort"
	"time"
)

type usageKey struct {
	databaseID string
	from       time.Time
	to         time.Time
	meter      Meter
}

var _ UsageStore = (*MemoryStore)(nil)

func (s *MemoryStore) ListUsageDatabases(_ context.Context, limit int) ([]Database, error) {
	if limit < 1 || limit > 100 {
		return nil, ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	items := make([]Database, 0)
	for _, database := range s.databases {
		if database.State == StateReady && database.ProviderResourceID != "" {
			items = append(items, cloneDatabase(database))
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].UpdatedAt.Equal(items[j].UpdatedAt) {
			return items[i].ID < items[j].ID
		}
		return items[i].UpdatedAt.Before(items[j].UpdatedAt)
	})
	if len(items) > limit {
		items = items[:limit]
	}
	return items, nil
}

func (s *MemoryStore) RecordUsage(_ context.Context, records []UsageRecord) error {
	if len(records) == 0 {
		return ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, record := range records {
		if err := record.Validate(); err != nil {
			return err
		}
		database, ok := s.databases[record.DatabaseID]
		if !ok || database.AccountID != record.AccountID || database.BackendID != record.BackendID || database.BackendFingerprint != record.BackendFingerprint {
			return ErrConflict
		}
		s.usage[usageKey{databaseID: record.DatabaseID, from: record.WindowFrom, to: record.WindowTo, meter: record.Meter}] = record
	}
	return nil
}

func (s *MemoryStore) UsageSnapshot(_ context.Context, accountID string, periodStart time.Time) (UsageSnapshot, error) {
	if accountID == "" || periodStart.IsZero() {
		return UsageSnapshot{}, ErrInvalid
	}
	periodStart = monthStart(periodStart)
	periodEnd := monthStart(periodStart.AddDate(0, 1, 0))
	s.mu.Lock()
	defer s.mu.Unlock()
	var snapshot UsageSnapshot
	snapshot.PeriodStart = periodStart
	for _, database := range s.databases {
		if database.AccountID == accountID && database.State == StateReady {
			snapshot.ReadyDatabases++
		}
	}
	for _, record := range s.usage {
		if record.AccountID != accountID || record.WindowFrom.Before(periodStart) || !record.WindowFrom.Before(periodEnd) {
			continue
		}
		database, ok := s.databases[record.DatabaseID]
		if !ok || database.State == StateDeleted {
			continue
		}
		var err error
		switch record.Meter {
		case MeterComputeUnitSeconds:
			snapshot.ComputeUnitSeconds, err = addUsage(snapshot.ComputeUnitSeconds, record.Quantity)
		case MeterStorageByteSeconds:
			snapshot.StorageByteSeconds, err = addUsage(snapshot.StorageByteSeconds, record.Quantity)
		case MeterHistoryByteSeconds:
			snapshot.HistoryByteSeconds, err = addUsage(snapshot.HistoryByteSeconds, record.Quantity)
		case MeterEgressBytes:
			snapshot.EgressBytes, err = addUsage(snapshot.EgressBytes, record.Quantity)
		}
		if err != nil {
			return UsageSnapshot{}, err
		}
		snapshot.CostMillicents, err = addUsage(snapshot.CostMillicents, record.CostMillicents)
		if err != nil {
			return UsageSnapshot{}, err
		}
		if record.ObservedAt.After(snapshot.LastObservedAt) {
			snapshot.LastObservedAt = record.ObservedAt
		}
	}
	return snapshot, nil
}
