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

func (s *MemoryStore) ListUsageDatabases(_ context.Context, after UsageDatabaseCursor, limit int) ([]Database, error) {
	if limit < 1 || limit > 100 {
		return nil, ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	items := make([]Database, 0)
	for _, database := range s.databases {
		if database.ProviderResourceID == "" {
			continue
		}
		if !after.isZero() && (database.UpdatedAt.Before(after.UpdatedAt) ||
			(database.UpdatedAt.Equal(after.UpdatedAt) && database.ID <= after.ID)) {
			continue
		}
		items = append(items, cloneDatabase(database))
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
	key, err := validateUsageRecords(records)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	first := records[0]
	database, ok := s.databases[first.DatabaseID]
	if !ok || database.AccountID != first.AccountID || database.BackendID != first.BackendID || database.BackendFingerprint != first.BackendFingerprint {
		return ErrConflict
	}
	if err := validateUsageDatabaseWindow(database, first); err != nil {
		return err
	}
	// Changing window size would overlap existing ledger periods and count
	// the same consumption twice. Require an explicit accounting migration.
	for existingKey := range s.usage {
		if s.usageProgress[key].CollectedUntil.IsZero() && existingKey.databaseID == first.DatabaseID && existingKey.to.Sub(existingKey.from) != key.window {
			return ErrConflict
		}
	}
	progress, err := advanceUsageProgress(s.usageProgress[key], first, database.CreatedAt)
	if err != nil {
		return err
	}
	for _, record := range records {
		recordKey := usageKey{databaseID: record.DatabaseID, from: record.WindowFrom.UTC(), to: record.WindowTo.UTC(), meter: record.Meter}
		if previous, ok := s.usage[recordKey]; !ok || !record.ObservedAt.Before(previous.ObservedAt) {
			s.usage[recordKey] = record
		}
	}
	s.usageProgress[key] = progress
	return nil
}

func (s *MemoryStore) UsageProgress(_ context.Context, accountID, databaseID string, window time.Duration) (UsageProgress, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	database, ok := s.databases[databaseID]
	if !ok || database.AccountID != accountID {
		return UsageProgress{}, ErrNotFound
	}
	return s.usageProgressLocked(databaseID, window), nil
}

func (s *MemoryStore) usageProgressLocked(databaseID string, window time.Duration) UsageProgress {
	progress := s.usageProgress[usageProgressKey{databaseID, window}]
	from := progress.CollectedUntil.Add(-recentUsageCorrectionWindows * window)
	if from.Before(progress.CollectedFrom) {
		from = progress.CollectedFrom
	}
	for _, record := range s.usage {
		if record.DatabaseID == databaseID && !record.WindowFrom.Before(from) && !record.WindowTo.After(progress.CollectedUntil) &&
			(progress.CorrectionObservedAt.IsZero() || record.ObservedAt.Before(progress.CorrectionObservedAt)) {
			progress.CorrectionObservedAt = record.ObservedAt
		}
	}
	return progress
}

func (s *MemoryStore) RecordSharedUsage(_ context.Context, accountID, databaseID, sourceID string, window time.Duration) error {
	if !validUsageWindow(window) || databaseID == sourceID {
		return ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	database, ok := s.databases[databaseID]
	source, sourceOK := s.databases[sourceID]
	if !ok || !sourceOK || database.AccountID != accountID || source.AccountID != accountID ||
		database.ProviderResourceID == "" || source.ProviderResourceID == "" || database.RestoreSourceDatabaseID == "" ||
		database.BackendID != source.BackendID || database.BackendFingerprint != source.BackendFingerprint {
		return ErrConflict
	}
	ancestor := database.RestoreSourceDatabaseID
	seen := map[string]bool{databaseID: true}
	for ancestor != sourceID {
		if ancestor == "" || seen[ancestor] {
			return ErrConflict
		}
		seen[ancestor] = true
		parent, exists := s.databases[ancestor]
		if !exists || parent.AccountID != accountID {
			return ErrConflict
		}
		ancestor = parent.RestoreSourceDatabaseID
	}
	s.usageProgress[usageProgressKey{databaseID, window}] = UsageProgress{Window: window, SourceDatabaseID: sourceID, UpdatedAt: time.Now().UTC()}
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
		if database.AccountID == accountID && (database.State == StateReady || database.ProviderResourceID != "") {
			if database.State == StateReady {
				snapshot.ReadyDatabases++
			}
			var progress UsageProgress
			accountingDatabase := database
			for key, candidate := range s.usageProgress {
				if key.databaseID == database.ID && candidate.UpdatedAt.After(progress.UpdatedAt) {
					progress = candidate
				}
			}
			if progress.SourceDatabaseID != "" {
				if source, ok := s.databases[progress.SourceDatabaseID]; ok {
					accountingDatabase = source
				}
				progress = s.usageProgressLocked(progress.SourceDatabaseID, progress.Window)
			} else {
				progress = s.usageProgressLocked(database.ID, progress.Window)
			}
			progress.Terminal = accountingDatabase.State == StateDeleted
			if accountingDatabase.DeletedAt != nil {
				progress.EndedAt = *accountingDatabase.DeletedAt
			}
			snapshot.Databases = append(snapshot.Databases, progress)
		}
	}
	for _, record := range s.usage {
		if record.AccountID != accountID || record.WindowFrom.Before(periodStart) || !record.WindowFrom.Before(periodEnd) {
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
	}
	for i, progress := range snapshot.Databases {
		if i == 0 || progress.ObservedAt.Before(snapshot.LastObservedAt) {
			snapshot.LastObservedAt = progress.ObservedAt
		}
	}
	return snapshot, nil
}
