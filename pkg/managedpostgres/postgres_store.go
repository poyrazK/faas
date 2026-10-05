package managedpostgres

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// PostgresStore is the production catalog adapter for managed PostgreSQL.
// The pool remains owned by the daemon and may be shared with other stores.
type PostgresStore struct {
	pool *pgxpool.Pool
}

func NewPostgresStore(pool *pgxpool.Pool) (*PostgresStore, error) {
	if pool == nil {
		return nil, ErrInvalid
	}
	return &PostgresStore{pool: pool}, nil
}

var _ Store = (*PostgresStore)(nil)

type databaseQueryer interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func (s *PostgresStore) Reserve(ctx context.Context, database Database, limit int) (Database, bool, error) {
	if err := validateReservation(database, limit); err != nil {
		return Database{}, false, err
	}
	accountID, err := postgresUUID(database.AccountID)
	if err != nil {
		return Database{}, false, err
	}
	databaseID, err := postgresUUID(database.ID)
	if err != nil {
		return Database{}, false, err
	}
	if database.RetryAt.IsZero() {
		database.RetryAt = database.CreatedAt
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Database{}, false, fmt.Errorf("managed postgres: begin reservation: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	q := new(sqlc.Queries)
	// Serialize tenant quotas and race safely with account deletion.
	if _, err := q.LockManagedPostgresLifecycleAccount(ctx, tx, accountID); err != nil {
		return Database{}, false, mapPostgresError(err)
	}
	row, err := q.FindManagedPostgresLifecycleDatabase(ctx, tx, sqlc.FindManagedPostgresLifecycleDatabaseParams{AccountID: accountID, Name: database.Name})
	if err == nil {
		existing := databaseFromSQL(row)
		if existing.EnvironmentCloneOperationID != "" {
			return Database{}, false, ErrConflict
		}
		if err := tx.Commit(ctx); err != nil {
			return Database{}, false, mapPostgresError(err)
		}
		return existing, false, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Database{}, false, mapPostgresError(err)
	}
	if database.RestoreSourceDatabaseID != "" {
		sourceID, err := postgresUUID(database.RestoreSourceDatabaseID)
		if err != nil {
			return Database{}, false, err
		}
		if _, err := q.LockManagedPostgresCustomerDatabase(ctx, tx, sqlc.LockManagedPostgresCustomerDatabaseParams{AccountID: accountID, ID: sourceID}); err != nil {
			return Database{}, false, mapPostgresError(err)
		}
		sourceRow, err := q.ReadManagedPostgresLifecycleRestoreSource(ctx, tx, sourceID)
		if err != nil {
			return Database{}, false, mapPostgresError(err)
		}
		source := databaseFromSQL(sourceRow)
		if source.AccountID != database.AccountID {
			return Database{}, false, ErrNotFound
		}
		if source.State != StateReady || source.ProviderResourceID == "" || databaseDataResource(source) != database.RestoreSourceResourceID {
			return Database{}, false, ErrConflict
		}
	}
	active, err := q.CountManagedPostgresLifecycleDatabases(ctx, tx, accountID)
	if err != nil {
		return Database{}, false, mapPostgresError(err)
	}
	if active >= int64(limit) {
		return Database{}, false, ErrQuotaExceeded
	}
	row, err = q.InsertManagedPostgresLifecycleDatabase(ctx, tx, sqlc.InsertManagedPostgresLifecycleDatabaseParams{
		ID: databaseID, AccountID: accountID, Name: database.Name, Region: database.Spec.Region,
		PostgresMajor: int16(database.Spec.PostgresMajor), ServiceClass: string(database.Spec.Class), Availability: string(database.Spec.Availability),
		ScaleToZero: database.Spec.ScaleToZero, StorageLimitBytes: database.Spec.StorageLimitBytes, RestoreWindowSeconds: database.Spec.RestoreWindowSeconds,
		BackendID: database.BackendID, BackendFingerprint: database.BackendFingerprint, RestoreSourceDatabaseID: databaseNullableUUID(database.RestoreSourceDatabaseID),
		RestoreSourceResourceID: databaseNullableText(database.RestoreSourceResourceID), RestorePointInTime: databaseNullableTime(database.RestorePointInTime),
		State: string(database.State), DesiredGeneration: database.DesiredGeneration, ObservedGeneration: database.ObservedGeneration,
		RetryAt: databaseNullableTime(database.RetryAt), CreatedAt: databaseNullableTime(database.CreatedAt), UpdatedAt: databaseNullableTime(database.UpdatedAt),
	})
	if err != nil {
		return Database{}, false, mapPostgresError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Database{}, false, mapPostgresError(err)
	}
	return databaseFromSQL(row), true, nil
}

func (s *PostgresStore) FindByName(ctx context.Context, accountID, name string) (Database, error) {
	account, err := postgresUUID(accountID)
	if err != nil || !ValidName(name) {
		return Database{}, ErrInvalid
	}
	row, err := new(sqlc.Queries).FindManagedPostgresLifecycleDatabase(ctx, s.pool, sqlc.FindManagedPostgresLifecycleDatabaseParams{AccountID: account, Name: name})
	return databaseFromSQL(row), mapPostgresError(err)
}

func (s *PostgresStore) Get(ctx context.Context, accountID, databaseID string) (Database, error) {
	account, err := postgresUUID(accountID)
	if err != nil {
		return Database{}, err
	}
	id, err := postgresUUID(databaseID)
	if err != nil {
		return Database{}, err
	}
	row, err := new(sqlc.Queries).GetManagedPostgresLifecycleDatabase(ctx, s.pool, sqlc.GetManagedPostgresLifecycleDatabaseParams{AccountID: account, ID: id})
	return databaseFromSQL(row), mapPostgresError(err)
}

func (s *PostgresStore) List(ctx context.Context, accountID string) ([]Database, error) {
	account, err := postgresUUID(accountID)
	if err != nil {
		return nil, err
	}
	rows, err := new(sqlc.Queries).ListManagedPostgresLifecycleDatabases(ctx, s.pool, account)
	return databasesFromSQL(rows), mapPostgresError(err)
}

func (s *PostgresStore) Due(ctx context.Context, includeProvisioning bool, limit int, now time.Time) ([]Database, error) {
	if limit < 1 || limit > 100 || now.IsZero() {
		return nil, ErrInvalid
	}
	rows, err := new(sqlc.Queries).DueManagedPostgresLifecycleDatabases(ctx, s.pool, sqlc.DueManagedPostgresLifecycleDatabasesParams{
		IncludeProvisioning: includeProvisioning, At: databaseNullableTime(now), RowLimit: int32(limit)})
	return databasesFromSQL(rows), mapPostgresError(err)
}

func (s *PostgresStore) Claim(ctx context.Context, accountID, databaseID, leaseToken string, operation State, now, leaseUntil time.Time) (Database, error) {
	if operation == StateDeleting {
		return s.ClaimDelete(ctx, accountID, databaseID, leaseToken, now, leaseUntil)
	}
	if leaseToken == "" || now.IsZero() || !leaseUntil.After(now) || operation != StateProvisioning {
		return Database{}, ErrInvalid
	}
	account, err := postgresUUID(accountID)
	if err != nil {
		return Database{}, err
	}
	id, err := postgresUUID(databaseID)
	if err != nil {
		return Database{}, err
	}
	q := new(sqlc.Queries)
	row, err := q.ClaimManagedPostgresLifecycleProvision(ctx, s.pool, sqlc.ClaimManagedPostgresLifecycleProvisionParams{
		AccountID: account, DatabaseID: id, LeaseToken: leaseToken, LeaseUntil: databaseNullableTime(leaseUntil), At: databaseNullableTime(now)})
	if !errors.Is(err, pgx.ErrNoRows) {
		return databaseFromSQL(row), mapPostgresError(err)
	}
	exists, err := q.ExistsManagedPostgresLifecycleDatabase(ctx, s.pool, sqlc.ExistsManagedPostgresLifecycleDatabaseParams{AccountID: account, ID: id})
	if err != nil {
		return Database{}, mapPostgresError(err)
	}
	if !exists {
		return Database{}, ErrNotFound
	}
	return Database{}, ErrConflict
}

// ClaimDelete locks the database before checking its dependants and changing
// lifecycle state. Binding reservations take a key-share lock on this row, and
// restore reservations take the same lock on their source, so a concurrent
// reservation either commits first and blocks deletion or observes deleting
// and fails before it can create a dependant.
func (s *PostgresStore) ClaimDelete(ctx context.Context, accountID, databaseID, leaseToken string, now, leaseUntil time.Time) (Database, error) {
	if leaseToken == "" || now.IsZero() || !leaseUntil.After(now) {
		return Database{}, ErrInvalid
	}
	account, err := postgresUUID(accountID)
	if err != nil {
		return Database{}, err
	}
	id, err := postgresUUID(databaseID)
	if err != nil {
		return Database{}, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Database{}, fmt.Errorf("managed postgres: begin delete claim: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	q := new(sqlc.Queries)
	row, err := q.LockManagedPostgresLifecycleDatabase(ctx, tx, sqlc.LockManagedPostgresLifecycleDatabaseParams{AccountID: account, ID: id})
	if err != nil {
		return Database{}, mapPostgresError(err)
	}
	current := databaseFromSQL(row)
	if current.State == StateDeleted || (!current.LeaseUntil.IsZero() && current.LeaseUntil.After(now)) {
		return Database{}, ErrConflict
	}
	dependants, err := q.ReadManagedPostgresLifecycleDependants(ctx, tx, id)
	if err != nil {
		return Database{}, mapPostgresError(err)
	}
	if dependants.HasBindings || dependants.HasRestoreDescendants || dependants.HasCloneSnapshotHolds || dependants.HasCloneWriteFenceHolds {
		return Database{}, ErrConflict
	}
	row, err = q.ClaimManagedPostgresLifecycleDelete(ctx, tx, sqlc.ClaimManagedPostgresLifecycleDeleteParams{
		AccountID: account, DatabaseID: id, LeaseToken: leaseToken, LeaseUntil: databaseNullableTime(leaseUntil), At: databaseNullableTime(now)})
	if err != nil {
		return Database{}, mapPostgresError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Database{}, mapPostgresError(err)
	}
	return databaseFromSQL(row), nil
}

func (s *PostgresStore) RecordProviderResource(ctx context.Context, databaseID, leaseToken, providerResourceID string, now time.Time) error {
	if leaseToken == "" || providerResourceID == "" || now.IsZero() {
		return ErrInvalid
	}
	id, err := postgresUUID(databaseID)
	if err != nil {
		return err
	}
	affected, err := new(sqlc.Queries).RecordManagedPostgresLifecycleResource(ctx, s.pool, sqlc.RecordManagedPostgresLifecycleResourceParams{
		DatabaseID: id, LeaseToken: leaseToken, ProviderResourceID: providerResourceID, At: databaseNullableTime(now)})
	if err != nil {
		return mapPostgresError(err)
	}
	if affected != 1 {
		return ErrConflict
	}
	return nil
}

func (s *PostgresStore) FinishProvision(ctx context.Context, databaseID, leaseToken string, now time.Time) (Database, error) {
	if leaseToken == "" || now.IsZero() {
		return Database{}, ErrInvalid
	}
	id, err := postgresUUID(databaseID)
	if err != nil {
		return Database{}, err
	}
	row, err := new(sqlc.Queries).FinishManagedPostgresLifecycleProvision(ctx, s.pool, sqlc.FinishManagedPostgresLifecycleProvisionParams{
		DatabaseID: id, LeaseToken: leaseToken, At: databaseNullableTime(now)})
	if errors.Is(err, pgx.ErrNoRows) {
		return Database{}, ErrConflict
	}
	return databaseFromSQL(row), mapPostgresError(err)
}

func (s *PostgresStore) Release(ctx context.Context, databaseID, leaseToken string, next State, errorCode string, now, retryAt time.Time) error {
	if leaseToken == "" || now.IsZero() || retryAt.Before(now) || !validErrorCode(errorCode) || (next != StateProvisioning && next != StateDeleting && next != StateFailed) {
		return ErrInvalid
	}
	id, err := postgresUUID(databaseID)
	if err != nil {
		return err
	}
	affected, err := new(sqlc.Queries).ReleaseManagedPostgresLifecycleLease(ctx, s.pool, sqlc.ReleaseManagedPostgresLifecycleLeaseParams{
		DatabaseID: id, LeaseToken: leaseToken, NextState: string(next), ErrorCode: errorCode, RetryAt: databaseNullableTime(retryAt), At: databaseNullableTime(now)})
	if err != nil {
		return mapPostgresError(err)
	}
	if affected != 1 {
		return ErrConflict
	}
	return nil
}

func (s *PostgresStore) FinishDelete(ctx context.Context, databaseID, leaseToken string, now time.Time) (Database, error) {
	if leaseToken == "" || now.IsZero() {
		return Database{}, ErrInvalid
	}
	id, err := postgresUUID(databaseID)
	if err != nil {
		return Database{}, err
	}
	row, err := new(sqlc.Queries).FinishManagedPostgresLifecycleDelete(ctx, s.pool, sqlc.FinishManagedPostgresLifecycleDeleteParams{
		DatabaseID: id, LeaseToken: leaseToken, At: databaseNullableTime(now)})
	if errors.Is(err, pgx.ErrNoRows) {
		return Database{}, ErrConflict
	}
	return databaseFromSQL(row), mapPostgresError(err)
}

func databasesFromSQL(rows []sqlc.ManagedPostgresDatabase) []Database {
	items := make([]Database, 0, len(rows))
	for _, row := range rows {
		items = append(items, databaseFromSQL(row))
	}
	return items
}

func databaseNullableTime(value time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: value, Valid: !value.IsZero()}
}
func databaseNullableText(value string) pgtype.Text {
	return pgtype.Text{String: value, Valid: value != ""}
}
func databaseNullableUUID(value string) pgtype.UUID {
	if value == "" {
		return pgtype.UUID{}
	}
	id, _ := postgresUUID(value)
	return id
}

func validateReservation(database Database, limit int) error {
	validFingerprint := regexp.MustCompile(`^[a-f0-9]{64}$`)
	if limit < 1 || limit > 100 || database.ID == "" || database.AccountID == "" ||
		!ValidName(database.Name) || database.Spec.Validate() != nil ||
		database.State != StateProvisioning || !ValidName(database.BackendID) ||
		!validFingerprint.MatchString(database.BackendFingerprint) ||
		database.DesiredGeneration < 1 || database.ObservedGeneration != 0 ||
		database.CreatedAt.IsZero() || database.UpdatedAt.IsZero() ||
		database.ProviderResourceID != "" || database.DataResourceID != "" || database.LeaseToken != "" || database.DeletedAt != nil || database.EnvironmentCloneOperationID != "" || database.AccountingRequired {
		return ErrInvalid
	}
	if database.RestoreSourceDatabaseID == "" && database.RestoreSourceResourceID == "" && database.RestorePointInTime.IsZero() {
		return nil
	}
	if database.RestoreSourceDatabaseID == "" || database.RestoreSourceResourceID == "" || database.RestorePointInTime.IsZero() {
		return ErrInvalid
	}
	if _, err := postgresUUID(database.RestoreSourceDatabaseID); err != nil {
		return err
	}
	return nil
}

func postgresUUID(value string) (pgtype.UUID, error) {
	parsed, err := uuid.Parse(value)
	if err != nil {
		return pgtype.UUID{}, ErrInvalid
	}
	return pgtype.UUID{Bytes: [16]byte(parsed), Valid: true}, nil
}

func mapPostgresError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) {
		return err
	}
	switch postgresError.Code {
	case pgerrcode.DeadlockDetected, pgerrcode.SerializationFailure:
		return ErrConflict
	case pgerrcode.UniqueViolation:
		return fmt.Errorf("%w: %s", ErrConflict, postgresError.ConstraintName)
	case pgerrcode.ForeignKeyViolation:
		return ErrNotFound
	case pgerrcode.CheckViolation:
		if postgresError.ConstraintName == "managed_postgres_cutover_conflict" || postgresError.ConstraintName == "managed_postgres_database_has_bindings" || postgresError.ConstraintName == "managed_postgres_database_has_restore_descendants" {
			return ErrConflict
		}
		return fmt.Errorf("%w: %s", ErrInvalid, postgresError.ConstraintName)
	default:
		return err
	}
}
