package managedpostgres

import (
	"context"
	"sort"
	"time"
)

var _ BranchExpiryStore = (*MemoryStore)(nil)

// ExpiredBranchesPage implements BranchExpiryStore (ADR-20261106103654).
func (s *MemoryStore) ExpiredBranchesPage(_ context.Context, now time.Time, after BranchExpiryCursor, limit int) ([]Database, error) {
	if limit < 1 || limit > 500 || now.IsZero() || after.ExpiresAt.IsZero() != (after.ID == "") {
		return nil, ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var expired []Database
	for _, database := range s.databases {
		if database.ExpiresAt.IsZero() || database.ExpiresAt.After(now) || database.State == StateDeleted ||
			database.State == StateDeleting && database.PurgeAfter.IsZero() {
			continue
		}
		if !after.ExpiresAt.IsZero() && (database.ExpiresAt.Before(after.ExpiresAt) ||
			database.ExpiresAt.Equal(after.ExpiresAt) && database.ID <= after.ID) {
			continue
		}
		expired = append(expired, cloneDatabase(database))
	}
	sort.Slice(expired, func(i, j int) bool {
		if !expired[i].ExpiresAt.Equal(expired[j].ExpiresAt) {
			return expired[i].ExpiresAt.Before(expired[j].ExpiresAt)
		}
		return expired[i].ID < expired[j].ID
	})
	if len(expired) > limit {
		expired = expired[:limit]
	}
	return expired, nil
}
