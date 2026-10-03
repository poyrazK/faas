package managedpostgres

import "time"

type usageProgressKey struct {
	databaseID string
	window     time.Duration
}

// A batch is one complete window, so its ledger and coverage can commit as a
// single unit. Validate everything before mutating either store.
func validateUsageRecords(records []UsageRecord) (usageProgressKey, error) {
	if len(records) == 0 {
		return usageProgressKey{}, ErrInvalid
	}
	first := records[0]
	window := first.WindowTo.Sub(first.WindowFrom)
	if window < time.Hour || window > 24*time.Hour || window%time.Second != 0 || !first.WindowFrom.UTC().Equal(first.WindowFrom.UTC().Truncate(window)) {
		return usageProgressKey{}, ErrInvalid
	}
	seen := make(map[Meter]bool, len(records))
	for _, record := range records {
		if err := record.Validate(); err != nil {
			return usageProgressKey{}, err
		}
		if record.AccountID != first.AccountID || record.DatabaseID != first.DatabaseID ||
			record.BackendID != first.BackendID || record.BackendFingerprint != first.BackendFingerprint ||
			!record.WindowFrom.Equal(first.WindowFrom) || !record.WindowTo.Equal(first.WindowTo) ||
			!record.ObservedAt.Equal(first.ObservedAt) || seen[record.Meter] {
			return usageProgressKey{}, ErrInvalid
		}
		seen[record.Meter] = true
	}
	return usageProgressKey{databaseID: first.DatabaseID, window: window}, nil
}

func advanceUsageProgress(previous UsageProgress, first UsageRecord, createdAt time.Time) (UsageProgress, error) {
	window := first.WindowTo.Sub(first.WindowFrom)
	if previous.SourceDatabaseID != "" {
		return UsageProgress{}, ErrConflict
	}
	if previous.CollectedUntil.IsZero() {
		if !createdAt.IsZero() && first.WindowFrom.After(createdAt.UTC().Truncate(window)) {
			return UsageProgress{}, ErrConflict
		}
		previous = UsageProgress{Window: window, CollectedFrom: first.WindowFrom.UTC()}
	} else if first.WindowFrom.After(previous.CollectedUntil) || first.WindowFrom.Before(previous.CollectedFrom) {
		return UsageProgress{}, ErrConflict
	}
	if first.WindowTo.After(previous.CollectedUntil) {
		previous.CollectedUntil = first.WindowTo.UTC()
	}
	if first.ObservedAt.After(previous.ObservedAt) {
		previous.ObservedAt = first.ObservedAt.UTC()
	}
	previous.UpdatedAt = time.Now().UTC()
	return previous, nil
}
