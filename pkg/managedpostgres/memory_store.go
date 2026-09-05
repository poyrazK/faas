package managedpostgres

import (
	"context"
	"sort"
	"sync"
	"time"
)

// MemoryStore is useful for unit tests and local wiring. Production adapters
// should enforce the same transitions transactionally in PostgreSQL.
type MemoryStore struct {
	mu        sync.Mutex
	databases map[string]Database
	names     map[string]string
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{databases: map[string]Database{}, names: map[string]string{}}
}

func (s *MemoryStore) Reserve(_ context.Context, database Database, limit int) (Database, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if limit < 1 {
		return Database{}, false, ErrInvalid
	}
	key := database.AccountID + "\x00" + database.Name
	if id, ok := s.names[key]; ok {
		return cloneDatabase(s.databases[id]), false, nil
	}
	if database.ID == "" || database.AccountID == "" || !ValidName(database.Name) || database.State != StateProvisioning || database.BackendID == "" || database.BackendFingerprint == "" {
		return Database{}, false, ErrInvalid
	}
	if _, exists := s.databases[database.ID]; exists {
		return Database{}, false, ErrConflict
	}
	active := 0
	for _, existing := range s.databases {
		if existing.AccountID == database.AccountID && existing.State != StateDeleted {
			active++
		}
	}
	if active >= limit {
		return Database{}, false, ErrQuotaExceeded
	}
	s.databases[database.ID] = cloneDatabase(database)
	s.names[key] = database.ID
	return cloneDatabase(database), true, nil
}

func (s *MemoryStore) FindByName(_ context.Context, accountID, name string) (Database, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, ok := s.names[accountID+"\x00"+name]
	if !ok {
		return Database{}, ErrNotFound
	}
	return cloneDatabase(s.databases[id]), nil
}

func (s *MemoryStore) Get(_ context.Context, accountID, databaseID string) (Database, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	database, ok := s.databases[databaseID]
	if !ok || database.AccountID != accountID {
		return Database{}, ErrNotFound
	}
	return cloneDatabase(database), nil
}

func (s *MemoryStore) List(_ context.Context, accountID string) ([]Database, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	items := make([]Database, 0)
	for _, database := range s.databases {
		if database.AccountID == accountID && database.State != StateDeleted {
			items = append(items, cloneDatabase(database))
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].CreatedAt.Equal(items[j].CreatedAt) {
			return items[i].ID < items[j].ID
		}
		return items[i].CreatedAt.Before(items[j].CreatedAt)
	})
	return items, nil
}

func (s *MemoryStore) Claim(_ context.Context, accountID, databaseID, leaseToken string, operation State, now, leaseUntil time.Time) (Database, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	database, ok := s.databases[databaseID]
	if !ok || database.AccountID != accountID {
		return Database{}, ErrNotFound
	}
	if leaseToken == "" || !leaseUntil.After(now) || (!database.LeaseUntil.IsZero() && database.LeaseUntil.After(now)) {
		return Database{}, ErrConflict
	}
	switch operation {
	case StateProvisioning:
		if database.State != StateProvisioning && database.State != StateFailed {
			return Database{}, ErrConflict
		}
	case StateDeleting:
		if database.State == StateDeleted {
			return Database{}, ErrConflict
		}
	default:
		return Database{}, ErrInvalid
	}
	database.State = operation
	database.LeaseToken = leaseToken
	database.LeaseUntil = leaseUntil
	database.UpdatedAt = now
	s.databases[databaseID] = database
	return cloneDatabase(database), nil
}

func (s *MemoryStore) RecordProviderResource(_ context.Context, databaseID, leaseToken, providerResourceID string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	database, ok := s.databases[databaseID]
	if !ok {
		return ErrNotFound
	}
	if database.State != StateProvisioning || database.LeaseToken != leaseToken || providerResourceID == "" {
		return ErrConflict
	}
	if database.ProviderResourceID != "" && database.ProviderResourceID != providerResourceID {
		return ErrConflict
	}
	database.ProviderResourceID = providerResourceID
	database.UpdatedAt = now
	s.databases[databaseID] = database
	return nil
}

func (s *MemoryStore) FinishProvision(_ context.Context, databaseID, leaseToken string, now time.Time) (Database, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	database, ok := s.databases[databaseID]
	if !ok {
		return Database{}, ErrNotFound
	}
	if database.State != StateProvisioning || database.LeaseToken != leaseToken || database.ProviderResourceID == "" {
		return Database{}, ErrConflict
	}
	database.State = StateReady
	database.ObservedGeneration = database.DesiredGeneration
	database.LastErrorCode = ""
	database.LeaseToken = ""
	database.LeaseUntil = time.Time{}
	database.UpdatedAt = now
	s.databases[databaseID] = database
	return cloneDatabase(database), nil
}

func (s *MemoryStore) Release(_ context.Context, databaseID, leaseToken string, next State, errorCode string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	database, ok := s.databases[databaseID]
	if !ok {
		return ErrNotFound
	}
	if database.LeaseToken != leaseToken {
		return ErrConflict
	}
	if next != StateProvisioning && next != StateDeleting && next != StateFailed {
		return ErrInvalid
	}
	database.State = next
	database.LastErrorCode = errorCode
	database.LeaseToken = ""
	database.LeaseUntil = time.Time{}
	database.UpdatedAt = now
	s.databases[databaseID] = database
	return nil
}

func (s *MemoryStore) FinishDelete(_ context.Context, databaseID, leaseToken string, now time.Time) (Database, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	database, ok := s.databases[databaseID]
	if !ok {
		return Database{}, ErrNotFound
	}
	if database.State != StateDeleting || database.LeaseToken != leaseToken {
		return Database{}, ErrConflict
	}
	database.State = StateDeleted
	database.LastErrorCode = ""
	database.LeaseToken = ""
	database.LeaseUntil = time.Time{}
	database.UpdatedAt = now
	database.DeletedAt = &now
	delete(s.names, database.AccountID+"\x00"+database.Name)
	s.databases[databaseID] = database
	return cloneDatabase(database), nil
}

func cloneDatabase(database Database) Database {
	if database.DeletedAt != nil {
		deletedAt := *database.DeletedAt
		database.DeletedAt = &deletedAt
	}
	return database
}
