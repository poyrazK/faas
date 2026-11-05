package managedpostgres

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/db"
)

var _ PreviewDatabaseStore = (*MemoryStore)(nil)

// SetPreviews implements PreviewDatabaseStore.
func (s *MemoryStore) SetPreviews(_ context.Context, accountID, databaseID string, mode PreviewMode, at time.Time) (Database, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	database, ok := s.databases[databaseID]
	if !ok || database.AccountID != accountID {
		return Database{}, ErrNotFound
	}
	if !database.Preview.IsZero() || database.State == StateDeleting || database.State == StateDeleted {
		return Database{}, ErrConflict
	}
	if mode == PreviewsOff {
		mode = ""
	}
	database.Previews = mode
	database.UpdatedAt = at
	s.databases[databaseID] = database
	return cloneDatabase(database), nil
}

// FindPreviewDatabase implements PreviewDatabaseStore.
func (s *MemoryStore) FindPreviewDatabase(_ context.Context, owner PreviewOwner) (Database, error) {
	if !owner.valid() {
		return Database{}, ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.previewDatabase(owner)
}

func (s *MemoryStore) previewDatabase(owner PreviewOwner) (Database, error) {
	for _, database := range s.databases {
		if database.Preview == owner && database.DeletedAt == nil {
			return cloneDatabase(database), nil
		}
	}
	return Database{}, ErrNotFound
}

// ReservePreview implements PreviewDatabaseStore.
func (s *MemoryStore) ReservePreview(ctx context.Context, database Database, limit int) (Database, bool, error) {
	// The owner and a copy's restore are checked apart: a copy restores its
	// own source.
	plain := database
	plain.Preview, plain.RestoreSourceDatabaseID, plain.RestoreSourceResourceID, plain.RestorePointInTime = PreviewOwner{}, "", "", time.Time{}
	if validateReservation(plain, 1) != nil || limit < 0 || !previewRestoreValid(database) || !database.Preview.valid() {
		return Database{}, false, ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	unlockIntent, intentErr := db.LockMemoryIntent(ctx)
	if intentErr != nil {
		return Database{}, false, intentErr
	}
	defer unlockIntent()
	if existing, err := s.previewDatabase(database.Preview); err == nil {
		return existing, false, nil
	}
	key := databaseNameKey(database)
	if _, ok := s.names[key]; ok {
		return Database{}, false, fmt.Errorf("%w: a database named %q already exists", ErrConflict, database.Name)
	}
	if _, exists := s.databases[database.ID]; exists {
		return Database{}, false, ErrConflict
	}
	source, ok := s.databases[database.Preview.SourceDatabaseID]
	if !ok || source.EnvironmentCloneOperationID != "" {
		return Database{}, false, ErrNotFound
	}
	if err := previewSourceAllows(source, database); err != nil {
		return Database{}, false, err
	}
	held := 0
	for _, existing := range s.databases {
		if !existing.Preview.IsZero() && existing.State != StateDeleted && s.sameQuotaOwner(ctx, existing, database) {
			held++
		}
	}
	if held >= limit {
		return Database{}, false, ErrPreviewDatabaseLimit
	}
	if database.RetryAt.IsZero() {
		database.RetryAt = database.CreatedAt
	}
	s.databases[database.ID] = cloneDatabase(database)
	s.billingSubjects[database.ID] = PolicySubject(ctx, database.AccountID)
	s.names[key] = database.ID
	return cloneDatabase(database), true, nil
}

// ListPreviewDatabases implements PreviewDatabaseStore.
func (s *MemoryStore) ListPreviewDatabases(_ context.Context, accountID, environmentID string, prNumber int) ([]Database, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var databases []Database
	for _, database := range s.databases {
		if database.AccountID == accountID && database.Preview.EnvironmentID == environmentID &&
			database.Preview.PRNumber == prNumber && database.DeletedAt == nil {
			databases = append(databases, cloneDatabase(database))
		}
	}
	slices.SortFunc(databases, func(a, b Database) int {
		if c := a.CreatedAt.Compare(b.CreatedAt); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	return databases, nil
}

// ListPreviewDatabasePage implements PreviewDatabaseStore.
func (s *MemoryStore) ListPreviewDatabasePage(_ context.Context, afterID string, limit int) ([]Database, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var databases []Database
	for _, database := range s.databases {
		if !database.Preview.IsZero() && database.DeletedAt == nil && database.ID > afterID {
			databases = append(databases, cloneDatabase(database))
		}
	}
	slices.SortFunc(databases, func(a, b Database) int { return strings.Compare(a.ID, b.ID) })
	if len(databases) > limit {
		databases = databases[:limit]
	}
	return databases, nil
}
