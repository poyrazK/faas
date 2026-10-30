package managedpostgres

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/db"
)

var _ DeletionScheduleStore = (*MemoryStore)(nil)

func (s *MemoryStore) ClaimScheduledDelete(ctx context.Context, accountID, databaseID, leaseToken string, now, leaseUntil, purgeAfter time.Time) (Database, error) {
	if leaseToken == "" || now.IsZero() || !leaseUntil.After(now) || !purgeAfter.After(leaseUntil) {
		return Database{}, ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	unlockIntent, intentErr := db.LockMemoryIntent(ctx)
	if intentErr != nil {
		return Database{}, intentErr
	}
	defer unlockIntent()
	database, ok := s.databases[databaseID]
	if !ok || database.AccountID != accountID {
		return Database{}, ErrNotFound
	}
	if s.databaseCutoverPinned(databaseID) || database.State != StateReady || (!database.LeaseUntil.IsZero() && database.LeaseUntil.After(now)) {
		return Database{}, ErrConflict
	}
	for _, candidate := range s.databases {
		if candidate.RestoreSourceDatabaseID == database.ID && candidate.State != StateDeleted {
			return Database{}, ErrConflict
		}
	}
	for _, binding := range s.bindings {
		if binding.DatabaseID == database.ID && binding.State != BindingStateDeleted {
			return Database{}, ErrConflict
		}
	}
	database.State = StateDeleting
	database.PurgeAfter = purgeAfter
	database.LeaseToken = leaseToken
	database.LeaseUntil = leaseUntil
	database.AttemptCount = 0
	database.LastErrorCode = ""
	database.RetryAt = purgeAfter
	database.UpdatedAt = now
	s.databases[databaseID] = database
	return cloneDatabase(database), nil
}

func (s *MemoryStore) ClaimUndelete(ctx context.Context, accountID, databaseID, leaseToken string, now, leaseUntil time.Time) (Database, error) {
	if leaseToken == "" || now.IsZero() || !leaseUntil.After(now) {
		return Database{}, ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	unlockIntent, intentErr := db.LockMemoryIntent(ctx)
	if intentErr != nil {
		return Database{}, intentErr
	}
	defer unlockIntent()
	database, ok := s.databases[databaseID]
	if !ok || database.AccountID != accountID {
		return Database{}, ErrNotFound
	}
	if !database.Restorable(now) || (!database.LeaseUntil.IsZero() && database.LeaseUntil.After(now)) {
		return Database{}, undeleteRefusal(database, now)
	}
	database.LeaseToken = leaseToken
	database.LeaseUntil = leaseUntil
	database.UpdatedAt = now
	s.databases[databaseID] = database
	return cloneDatabase(database), nil
}

func (s *MemoryStore) CancelScheduledDelete(ctx context.Context, databaseID, leaseToken string, now time.Time) (Database, error) {
	if leaseToken == "" || now.IsZero() {
		return Database{}, ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	unlockIntent, intentErr := db.LockMemoryIntent(ctx)
	if intentErr != nil {
		return Database{}, intentErr
	}
	defer unlockIntent()
	database, ok := s.databases[databaseID]
	if !ok || database.State != StateDeleting || database.PurgeAfter.IsZero() || database.LeaseToken != leaseToken || !database.LeaseUntil.After(now) {
		return Database{}, ErrConflict
	}
	database.State = StateReady
	database.PurgeAfter = time.Time{}
	database.LeaseToken = ""
	database.LeaseUntil = time.Time{}
	database.AttemptCount = 0
	database.LastErrorCode = ""
	database.RetryAt = now
	database.UpdatedAt = now
	s.databases[databaseID] = database
	return cloneDatabase(database), nil
}
