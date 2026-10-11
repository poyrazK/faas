package managedpostgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func validDataResourceID(value string) bool {
	return validOpaqueID(value)
}

// Legacy ordinary database operations retain their original selector. Full
// stage cloning independently requires a recorded, nonempty DataResourceID.
func databaseDataResource(database Database) string {
	if database.DataResourceID != "" {
		return database.DataResourceID
	}
	return database.ProviderResourceID
}

func validateDataResourceObservation(database Database, observed ObservedDatabase) error {
	if !validDataResourceID(observed.DataResourceID) || observed.Status != ProviderStatusReady ||
		observed.Spec != database.Spec || observed.ProviderResourceID != database.ProviderResourceID ||
		database.ProviderResourceID == "" || database.DesiredGeneration < 1 ||
		database.DataResourceID != "" && database.DataResourceID != observed.DataResourceID {
		return ErrConflict
	}
	return nil
}

func (s *MemoryStore) FinishProvisionWithDataResource(_ context.Context, expected Database, observed ObservedDatabase, now time.Time) (Database, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	database, ok := s.databases[expected.ID]
	if !ok {
		return Database{}, ErrNotFound
	}
	if now.IsZero() || database.AccountID != expected.AccountID || database.State != StateProvisioning || database.LeaseToken == "" ||
		database.LeaseToken != expected.LeaseToken || !database.LeaseUntil.After(now) || database.DeletedAt != nil ||
		database.EnvironmentCloneOperationID != "" || database.BackendID != expected.BackendID || database.BackendFingerprint != expected.BackendFingerprint ||
		database.DesiredGeneration != expected.DesiredGeneration || database.Spec != expected.Spec {
		return Database{}, ErrConflict
	}
	if err := validateDataResourceObservation(database, observed); err != nil {
		return Database{}, err
	}
	database.DataResourceID, database.State, database.ObservedGeneration = observed.DataResourceID, StateReady, database.DesiredGeneration
	database.LastErrorCode, database.LeaseToken, database.AttemptCount = "", "", 0
	database.LeaseUntil, database.RetryAt, database.UpdatedAt = time.Time{}, now, now
	s.databases[database.ID] = database
	return cloneDatabase(database), nil
}

func (s *PostgresStore) FinishProvisionWithDataResource(ctx context.Context, database Database, observed ObservedDatabase, now time.Time) (Database, error) {
	if err := validateDataResourceObservation(database, observed); err != nil {
		return Database{}, err
	}
	if now.IsZero() || database.LeaseToken == "" || database.EnvironmentCloneOperationID != "" {
		return Database{}, ErrInvalid
	}
	id, err := postgresUUID(database.ID)
	if err != nil {
		return Database{}, err
	}
	account, err := postgresUUID(database.AccountID)
	if err != nil {
		return Database{}, err
	}
	spec, err := json.Marshal(observed.Spec)
	if err != nil {
		return Database{}, ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Database{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	q := new(sqlc.Queries)
	actual, err := q.LockManagedPostgresLifecycleDatabase(ctx, tx, sqlc.LockManagedPostgresLifecycleDatabaseParams{AccountID: account, ID: id})
	if err != nil {
		return Database{}, mapPostgresError(err)
	}
	// Read the server clock after acquiring the row lock. UPDATE predicates
	// evaluated before waiting on an unchanged row are not lease authority.
	clock, err := q.ReadProjectEnvironmentCloneDatabaseReservationTime(ctx, tx)
	if err != nil {
		return Database{}, err
	}
	if !actual.LeaseUntil.Valid || !actual.LeaseUntil.Time.After(clock.Time) {
		return Database{}, ErrConflict
	}
	row, err := q.FinishManagedPostgresLifecycleDataProvision(ctx, tx, sqlc.FinishManagedPostgresLifecycleDataProvisionParams{
		DatabaseID: id, AccountID: account, LeaseToken: database.LeaseToken, At: pgtype.Timestamptz{Time: clock.Time, Valid: true},
		ProviderResourceID: database.ProviderResourceID, DataResourceID: observed.DataResourceID, BackendID: database.BackendID,
		BackendFingerprint: database.BackendFingerprint, Spec: spec, Generation: database.DesiredGeneration,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Database{}, ErrConflict
	}
	if err != nil {
		return Database{}, mapPostgresError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Database{}, mapPostgresError(err)
	}
	return databaseFromSQL(row), nil
}

var _ DataResourceProvisionStore = (*MemoryStore)(nil)
var _ DataResourceProvisionStore = (*PostgresStore)(nil)
