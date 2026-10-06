package managedpostgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ ResizeStore = (*PostgresStore)(nil)

func resizeFromSQL(row sqlc.ManagedPostgresResize) (ResizeOperation, error) {
	operation := ResizeOperation{ID: cutoverUUID(row.ID), AccountID: cutoverUUID(row.AccountID), DatabaseID: cutoverUUID(row.DatabaseID),
		BackendID: row.BackendID, BackendFingerprint: row.BackendFingerprint, ProviderResourceID: row.ProviderResourceID,
		DataResourceID: row.DataResourceID, TargetClass: ServiceClass(row.TargetClass), Generation: row.Generation, State: ResizeState(row.State),
		CreatedAt: bindingDeliveryTime(row.CreatedAt), CompletedAt: bindingDeliveryTime(row.CompletedAt)}
	if json.Unmarshal(row.SourceSpec, &operation.SourceSpec) != nil || operation.SourceSpec.Validate() != nil || operation.TargetSpec().Validate() != nil {
		return ResizeOperation{}, ErrUnavailable
	}
	return operation, nil
}

func (s *PostgresStore) GetResize(ctx context.Context, account, id string) (ResizeOperation, error) {
	accountID, err := postgresUUID(account)
	if err != nil {
		return ResizeOperation{}, err
	}
	operationID, err := postgresUUID(id)
	if err != nil {
		return ResizeOperation{}, err
	}
	row, err := sqlc.New().GetManagedPostgresResize(ctx, s.pool, sqlc.GetManagedPostgresResizeParams{AccountID: accountID, ID: operationID})
	if err != nil {
		return ResizeOperation{}, mapPostgresError(err)
	}
	operation, err := resizeFromSQL(row)
	if err == nil && operation.State == ResizePending {
		database, readErr := s.Get(ctx, account, operation.DatabaseID)
		if readErr != nil {
			return ResizeOperation{}, readErr
		}
		operation.LastErrorCode = database.LastErrorCode
	}
	return operation, err
}

func (s *PostgresStore) ActiveResize(ctx context.Context, account, database string) (ResizeOperation, error) {
	accountID, err := postgresUUID(account)
	if err != nil {
		return ResizeOperation{}, err
	}
	databaseID, err := postgresUUID(database)
	if err != nil {
		return ResizeOperation{}, err
	}
	row, err := sqlc.New().ActiveManagedPostgresResize(ctx, s.pool, sqlc.ActiveManagedPostgresResizeParams{AccountID: accountID, DatabaseID: databaseID})
	if err != nil {
		return ResizeOperation{}, mapPostgresError(err)
	}
	return resizeFromSQL(row)
}

func (s *PostgresStore) ReserveResize(ctx context.Context, expected Database, operation ResizeOperation, now time.Time) (ResizeOperation, error) {
	if !validResizeOperation(operation) || now.IsZero() || !resizeSourceMatches(expected, operation) {
		return ResizeOperation{}, ErrInvalid
	}
	account, err := postgresUUID(operation.AccountID)
	if err != nil {
		return ResizeOperation{}, err
	}
	id, err := postgresUUID(operation.DatabaseID)
	if err != nil {
		return ResizeOperation{}, err
	}
	requestID, err := postgresUUID(operation.ID)
	if err != nil {
		return ResizeOperation{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ResizeOperation{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	q := sqlc.New()
	if _, err = q.LockManagedPostgresLifecycleAccount(ctx, tx, account); err != nil {
		return ResizeOperation{}, mapPostgresError(err)
	}
	row, err := q.GetManagedPostgresResize(ctx, tx, sqlc.GetManagedPostgresResizeParams{AccountID: account, ID: requestID})
	if err == nil {
		existing, err := resizeFromSQL(row)
		if err != nil {
			return ResizeOperation{}, err
		}
		if existing.DatabaseID != operation.DatabaseID || existing.TargetClass != operation.TargetClass {
			return ResizeOperation{}, ErrConflict
		}
		return existing, mapPostgresError(tx.Commit(ctx))
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return ResizeOperation{}, mapPostgresError(err)
	}
	current, err := q.LockManagedPostgresLifecycleDatabase(ctx, tx, sqlc.LockManagedPostgresLifecycleDatabaseParams{AccountID: account, ID: id})
	if err != nil {
		return ResizeOperation{}, mapPostgresError(err)
	}
	clock, err := q.ReadProjectEnvironmentCloneDatabaseReservationTime(ctx, tx)
	if err != nil {
		return ResizeOperation{}, err
	}
	database := databaseFromSQL(current)
	if database.State != StateReady || !resizeSourceMatches(database, operation) || database.DesiredGeneration != expected.DesiredGeneration ||
		database.ObservedGeneration != database.DesiredGeneration || operation.Generation != database.DesiredGeneration+1 ||
		database.LeaseUntil.After(clock.Time) || current.CutoverID.Valid || current.CloneResourceRole != "target" || database.DeletedAt != nil {
		return ResizeOperation{}, ErrConflict
	}
	conflicts, err := q.ReadManagedPostgresResizeConflicts(ctx, tx, id)
	if err != nil {
		return ResizeOperation{}, mapPostgresError(err)
	}
	holds, err := q.ReadManagedPostgresLifecycleDependants(ctx, tx, id)
	if err != nil {
		return ResizeOperation{}, mapPostgresError(err)
	}
	if conflicts.UnfinishedBindings || conflicts.UnfinishedRestores || holds.HasCloneSnapshotHolds || holds.HasCloneWriteFenceHolds {
		return ResizeOperation{}, ErrConflict
	}
	if _, err = q.BeginManagedPostgresResizeDatabase(ctx, tx, sqlc.BeginManagedPostgresResizeDatabaseParams{ID: id, At: clock, SourceGeneration: database.DesiredGeneration}); err != nil {
		return ResizeOperation{}, mapPostgresError(err)
	}
	source, _ := json.Marshal(operation.SourceSpec)
	row, err = q.InsertManagedPostgresResize(ctx, tx, sqlc.InsertManagedPostgresResizeParams{ID: requestID, AccountID: account, DatabaseID: id,
		BackendID: operation.BackendID, BackendFingerprint: operation.BackendFingerprint, ProviderResourceID: operation.ProviderResourceID,
		DataResourceID: operation.DataResourceID, SourceSpec: source, TargetClass: string(operation.TargetClass), Generation: operation.Generation, CreatedAt: clock})
	if err != nil {
		return ResizeOperation{}, mapPostgresError(err)
	}
	if err = tx.Commit(ctx); err != nil {
		return ResizeOperation{}, mapPostgresError(err)
	}
	return resizeFromSQL(row)
}

func (s *PostgresStore) claimResize(ctx context.Context, account, database, token string, now, until time.Time) (Database, error) {
	accountID, err := postgresUUID(account)
	if err != nil {
		return Database{}, err
	}
	id, err := postgresUUID(database)
	if err != nil {
		return Database{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Database{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	q := sqlc.New()
	if _, err = q.LockManagedPostgresLifecycleDatabase(ctx, tx, sqlc.LockManagedPostgresLifecycleDatabaseParams{AccountID: accountID, ID: id}); err != nil {
		return Database{}, mapPostgresError(err)
	}
	clock, err := q.ReadProjectEnvironmentCloneDatabaseReservationTime(ctx, tx)
	if err != nil {
		return Database{}, err
	}
	if !until.After(clock.Time) {
		return Database{}, ErrConflict
	}
	row, err := q.ClaimManagedPostgresResize(ctx, tx, sqlc.ClaimManagedPostgresResizeParams{Account: accountID, ID: id, LeaseToken: token, At: clock, Until: databaseNullableTime(until)})
	if errors.Is(err, pgx.ErrNoRows) {
		return Database{}, ErrConflict
	}
	if err != nil {
		return Database{}, mapPostgresError(err)
	}
	if err = tx.Commit(ctx); err != nil {
		return Database{}, mapPostgresError(err)
	}
	return databaseFromSQL(row), nil
}

func (s *PostgresStore) FinishResize(ctx context.Context, expected Database, operation ResizeOperation, observed ObservedDatabase, now time.Time) (Database, error) {
	if now.IsZero() || expected.LeaseToken == "" || validateResizeObservation(operation, observed) != nil || observed.Status != ProviderStatusReady || observed.Spec != operation.TargetSpec() {
		return Database{}, ErrConflict
	}
	account, err := postgresUUID(expected.AccountID)
	if err != nil {
		return Database{}, err
	}
	id, err := postgresUUID(expected.ID)
	if err != nil {
		return Database{}, err
	}
	requestID, err := postgresUUID(operation.ID)
	if err != nil {
		return Database{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Database{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	q := sqlc.New()
	row, err := q.LockManagedPostgresLifecycleDatabase(ctx, tx, sqlc.LockManagedPostgresLifecycleDatabaseParams{AccountID: account, ID: id})
	if err != nil {
		return Database{}, mapPostgresError(err)
	}
	clock, err := q.ReadProjectEnvironmentCloneDatabaseReservationTime(ctx, tx)
	if err != nil {
		return Database{}, err
	}
	active, err := q.ActiveManagedPostgresResize(ctx, tx, sqlc.ActiveManagedPostgresResizeParams{AccountID: account, DatabaseID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return Database{}, ErrConflict
	}
	if err != nil {
		return Database{}, mapPostgresError(err)
	}
	actual, err := resizeFromSQL(active)
	if err != nil {
		return Database{}, err
	}
	database := databaseFromSQL(row)
	if validateResizeObservation(actual, observed) != nil || actual.ID != operation.ID || actual.Generation != operation.Generation || actual.TargetSpec() != operation.TargetSpec() ||
		!resizeSourceMatches(database, actual) || !resizeSourceMatches(expected, actual) || database.State != StateUpdating ||
		database.DesiredGeneration != actual.Generation || expected.DesiredGeneration != actual.Generation || database.ObservedGeneration != actual.Generation-1 ||
		database.LeaseToken != expected.LeaseToken || !database.LeaseUntil.After(clock.Time) {
		return Database{}, ErrConflict
	}
	changed, err := q.CompleteManagedPostgresResize(ctx, tx, sqlc.CompleteManagedPostgresResizeParams{ID: requestID, Database: id, Generation: actual.Generation, At: clock})
	if err != nil {
		return Database{}, mapPostgresError(err)
	}
	if changed != 1 {
		return Database{}, ErrConflict
	}
	row, err = q.FinishManagedPostgresResizeDatabase(ctx, tx, sqlc.FinishManagedPostgresResizeDatabaseParams{ID: id, Account: account, Token: expected.LeaseToken, Generation: actual.Generation, TargetClass: string(actual.TargetClass), At: clock})
	if errors.Is(err, pgx.ErrNoRows) {
		return Database{}, ErrConflict
	}
	if err != nil {
		return Database{}, mapPostgresError(err)
	}
	if err = tx.Commit(ctx); err != nil {
		return Database{}, mapPostgresError(err)
	}
	return databaseFromSQL(row), nil
}
