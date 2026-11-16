package managedpostgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// ADR-20261031193635 branch lifecycle: extending an environment branch moves
// the end of its database copies with it.

// BranchExpiryMover is the optional store capability MoveBranchExpiry needs.
type BranchExpiryMover interface {
	// MoveBranchExpiry sets an undeleted restore's expiry to next when it
	// still ends at current. ErrConflict means it moved or was deleted.
	MoveBranchExpiry(ctx context.Context, databaseID string, current, next time.Time) (Database, error)
}

var (
	_ BranchExpiryMover = (*MemoryStore)(nil)
	_ BranchExpiryMover = (*PostgresStore)(nil)
)

// MoveBranchExpiry moves the end of a restore that already ends (a branch).
// The new end must be in the future, at most MaxBranchTTL after the copy's
// creation, and, for a copy in the plan's branch allowance, at most
// api.ManagedPostgresAllowanceBranchTTLMax after it: moving it out of the
// allowance would put it on the database limit unreserved (ErrQuotaExceeded).
func (s *Service) MoveBranchExpiry(ctx context.Context, accountID, databaseID string, expiresAt time.Time) (Database, error) {
	mover, ok := s.store.(BranchExpiryMover)
	if !ok {
		return Database{}, ErrUnsupported
	}
	database, err := customerDatabase(ctx, s.store, accountID, databaseID)
	if err != nil {
		return Database{}, err
	}
	if database.RestoreSourceDatabaseID == "" || database.ExpiresAt.IsZero() ||
		database.State == StateDeleting || database.State == StateDeleted {
		return Database{}, ErrConflict
	}
	expiresAt = expiresAt.UTC().Truncate(time.Microsecond)
	if !expiresAt.After(s.now()) || expiresAt.After(database.CreatedAt.Add(MaxBranchTTL)) {
		return Database{}, ErrInvalid
	}
	if database.InBranchAllowance() && expiresAt.After(database.CreatedAt.Add(api.ManagedPostgresAllowanceBranchTTLMax)) {
		return Database{}, ErrQuotaExceeded
	}
	if expiresAt.Equal(database.ExpiresAt) {
		return database, nil
	}
	return mover.MoveBranchExpiry(ctx, database.ID, database.ExpiresAt, expiresAt)
}

func (s *MemoryStore) MoveBranchExpiry(_ context.Context, databaseID string, current, next time.Time) (Database, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	database, ok := s.databases[databaseID]
	if !ok || database.DeletedAt != nil || database.State == StateDeleting || database.State == StateDeleted ||
		database.RestoreSourceDatabaseID == "" || !database.ExpiresAt.Equal(current) {
		return Database{}, ErrConflict
	}
	database.ExpiresAt = next
	database.UpdatedAt = time.Now().UTC()
	s.databases[databaseID] = database
	return cloneDatabase(database), nil
}

func (s *PostgresStore) MoveBranchExpiry(ctx context.Context, databaseID string, current, next time.Time) (Database, error) {
	id := databaseNullableUUID(databaseID)
	if !id.Valid {
		return Database{}, ErrNotFound
	}
	row, err := new(sqlc.Queries).MoveManagedPostgresBranchExpiry(ctx, s.catalog, sqlc.MoveManagedPostgresBranchExpiryParams{
		ID: id, ExpiresAt: databaseNullableTime(next), CurrentExpiresAt: databaseNullableTime(current),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Database{}, ErrConflict
	}
	if err != nil {
		return Database{}, mapPostgresError(err)
	}
	return databaseFromSQL(row), nil
}
