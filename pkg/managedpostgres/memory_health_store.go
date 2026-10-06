package managedpostgres

import (
	"context"
	"time"
)

type memoryHealthEntry struct {
	claim       HealthClaim
	snapshot    HealthSnapshot
	nextCheckAt time.Time
}

func healthIdentityMatches(a, b Database) bool {
	return a.ID == b.ID && a.AccountID == b.AccountID && a.ProviderResourceID == b.ProviderResourceID &&
		a.BackendID == b.BackendID && a.BackendFingerprint == b.BackendFingerprint && a.DesiredGeneration == b.DesiredGeneration
}

func (s *MemoryStore) ClaimHealthCheck(_ context.Context, token string, now, until time.Time) (HealthClaim, error) {
	if token == "" || now.IsZero() || !until.After(now) {
		return HealthClaim{}, ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var selected Database
	var due time.Time
	for _, database := range s.databases {
		if database.State != StateReady || database.ProviderResourceID == "" {
			continue
		}
		next := database.CreatedAt
		if entry, ok := s.health[database.ID]; ok {
			if entry.claim.LeaseUntil.After(now) {
				continue
			}
			next = entry.nextCheckAt
			if !healthIdentityMatches(entry.claim.Database, database) {
				next = database.CreatedAt
			}
		}
		if next.After(now) {
			continue
		}
		if selected.ID == "" || next.Before(due) || (next.Equal(due) && database.ID < selected.ID) {
			selected, due = database, next
		}
	}
	if selected.ID == "" {
		return HealthClaim{}, ErrNotFound
	}
	entry := s.health[selected.ID]
	if !healthIdentityMatches(entry.claim.Database, selected) {
		entry = memoryHealthEntry{}
	}
	entry.claim.Database, entry.claim.LeaseToken, entry.claim.LeaseUntil = cloneDatabase(selected), token, until
	s.health[selected.ID] = entry
	return entry.claim, nil
}

func (s *MemoryStore) FinishHealthCheck(_ context.Context, claim HealthClaim, result HealthResult) error {
	if err := validateHealthResult(result); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.health[claim.Database.ID]
	database := s.databases[claim.Database.ID]
	if !ok || entry.claim.LeaseToken != claim.LeaseToken || claim.LeaseToken == "" ||
		!entry.claim.LeaseUntil.After(result.CheckedAt) || database.State != StateReady || !healthIdentityMatches(database, claim.Database) {
		return ErrConflict
	}
	lastSuccess := entry.snapshot.LastSuccessAt
	entry.snapshot = result.HealthSnapshot
	entry.snapshot.LastSuccessAt = lastSuccess
	if result.Succeeded {
		entry.snapshot.LastSuccessAt = result.CheckedAt
	}
	entry.claim.LeaseToken, entry.claim.LeaseUntil = "", time.Time{}
	entry.claim.AttemptCount++
	if entry.claim.AttemptCount > 20 {
		entry.claim.AttemptCount = 20
	}
	if result.Succeeded {
		entry.claim.AttemptCount = 0
	}
	entry.nextCheckAt = result.NextCheckAt
	s.health[database.ID] = entry
	return nil
}

func (s *MemoryStore) ReadHealthSnapshots(_ context.Context, accountID string, ids []string) (map[string]HealthSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows := make(map[string]HealthSnapshot)
	for _, id := range ids {
		database, ok := s.databases[id]
		if !ok || database.AccountID != accountID || database.State != StateReady {
			continue
		}
		entry, ok := s.health[id]
		if ok && healthIdentityMatches(database, entry.claim.Database) {
			rows[id] = entry.snapshot
		}
	}
	return rows, nil
}

func (s *MemoryStore) CountDatabaseHealth(_ context.Context, cutoff, now time.Time) (HealthCounts, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var counts HealthCounts
	for _, database := range s.databases {
		if database.State != StateReady {
			continue
		}
		snapshot := HealthSnapshot{}
		if entry, ok := s.health[database.ID]; ok && healthIdentityMatches(database, entry.claim.Database) {
			snapshot = entry.snapshot
		}
		switch {
		case snapshot.CheckedAt.IsZero() && !database.CreatedAt.Before(cutoff):
			counts.Unknown++
		case snapshot.CheckedAt.IsZero() || snapshot.CheckedAt.Before(cutoff) || snapshot.CheckedAt.After(now):
			counts.Stale++
		case snapshot.LastErrorCode != "" || snapshot.ProviderStatus != "ready" || snapshot.ComputeState == ComputeStateUnknown:
			counts.Degraded++
		default:
			counts.Healthy++
		}
	}
	return counts, nil
}
