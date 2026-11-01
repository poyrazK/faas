package managedpostgres

import (
	"context"
	"sort"
	"time"
)

// RestoreSlots implements RestoreSlotStore (ADR-20261031123259).
func (s *MemoryStore) RestoreSlots(_ context.Context, limit int) ([]RestoreSlotHolder, error) {
	if limit < 1 || limit > 500 {
		return nil, ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var holders []RestoreSlotHolder
	for _, database := range s.databases {
		if !database.RestoreSlotUntil.IsZero() && database.State != StateDeleted {
			holders = append(holders, RestoreSlotHolder{Database: cloneDatabase(database), SubjectID: s.billingSubject(database)})
		}
	}
	sort.Slice(holders, func(i, j int) bool {
		a, b := holders[i].Database, holders[j].Database
		if !a.RestoreSlotUntil.Equal(b.RestoreSlotUntil) {
			return a.RestoreSlotUntil.Before(b.RestoreSlotUntil)
		}
		return a.ID < b.ID
	})
	if len(holders) > limit {
		holders = holders[:limit]
	}
	return holders, nil
}

// CountSubjectDatabases implements RestoreSlotStore.
func (s *MemoryStore) CountSubjectDatabases(_ context.Context, subjectID string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var count int64
	for _, database := range s.databases {
		if database.State != StateDeleted && s.billingSubject(database) == subjectID {
			count++
		}
	}
	return count, nil
}

// ClearRestoreSlot implements RestoreSlotStore.
func (s *MemoryStore) ClearRestoreSlot(_ context.Context, databaseID string, now time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	database, ok := s.databases[databaseID]
	if !ok || database.RestoreSlotUntil.IsZero() {
		return false, nil
	}
	database.RestoreSlotUntil = time.Time{}
	database.UpdatedAt = now
	s.databases[databaseID] = database
	return true, nil
}
