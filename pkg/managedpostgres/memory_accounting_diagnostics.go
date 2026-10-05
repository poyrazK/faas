package managedpostgres

import (
	"context"
	"sort"
)

var _ AccountingDiagnosticsStore = (*MemoryStore)(nil)

func (s *MemoryStore) ListAccountingCoverage(ctx context.Context, accountID, afterID string, limit int) ([]AccountingCoverage, error) {
	if accountID == "" || limit < 1 || limit > 101 {
		return nil, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	items := make([]AccountingCoverage, 0)
	for _, d := range s.databases {
		if d.AccountID == accountID && d.ID > afterID && (d.State == StateReady || d.ProviderResourceID != "" || d.AccountingRequired) {
			items = append(items, s.accountingCoverageLocked(d))
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].DatabaseID < items[j].DatabaseID })
	if len(items) > limit {
		items = items[:limit]
	}
	return items, nil
}

func (s *MemoryStore) accountingCoverageLocked(database Database) AccountingCoverage {
	var progress UsageProgress
	root := database
	for key, candidate := range s.usageProgress {
		if key.databaseID == database.ID && (candidate.UpdatedAt.After(progress.UpdatedAt) ||
			(candidate.UpdatedAt.Equal(progress.UpdatedAt) && candidate.Window > progress.Window)) {
			progress = candidate
		}
	}
	if progress.SourceDatabaseID != "" {
		if source, ok := s.databases[progress.SourceDatabaseID]; ok {
			root = source
		}
		progress = s.usageProgressLocked(progress.SourceDatabaseID, progress.Window)
	} else {
		progress = s.usageProgressLocked(database.ID, progress.Window)
	}
	progress.Terminal = root.State == StateDeleted
	progress.Unresolved = database.AccountingRequired && database.ProviderResourceID == ""
	if root.DeletedAt != nil {
		progress.EndedAt = *root.DeletedAt
	}
	return AccountingCoverage{DatabaseID: database.ID, Name: database.Name, State: database.State,
		AccountingRequired: database.AccountingRequired, IdentityKnown: database.ProviderResourceID != "",
		AccountingDatabaseID: root.ID, CreatedAt: root.CreatedAt, LeaseUntil: database.LeaseUntil, Progress: progress}
}
