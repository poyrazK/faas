package managedpostgres

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ PreviewDatabaseStore = (*PostgresStore)(nil)

// SetPreviews implements PreviewDatabaseStore. A database being deleted, or
// one a preview environment owns, is refused with ErrConflict.
func (s *PostgresStore) SetPreviews(ctx context.Context, accountID, databaseID string, mode PreviewMode, at time.Time) (Database, error) {
	account, err := postgresUUID(accountID)
	if err != nil {
		return Database{}, err
	}
	id, err := postgresUUID(databaseID)
	if err != nil {
		return Database{}, err
	}
	if mode == "" {
		mode = PreviewsOff
	}
	row, err := new(sqlc.Queries).SetManagedPostgresDatabasePreviews(ctx, s.catalog, sqlc.SetManagedPostgresDatabasePreviewsParams{
		Previews: string(mode), At: databaseNullableTime(at), ID: id, AccountID: account})
	if errors.Is(err, pgx.ErrNoRows) {
		return Database{}, ErrConflict
	}
	return databaseFromSQL(row), mapPostgresError(err)
}

// FindPreviewDatabase implements PreviewDatabaseStore.
func (s *PostgresStore) FindPreviewDatabase(ctx context.Context, owner PreviewOwner) (Database, error) {
	params, err := previewOwnerParams(owner)
	if err != nil {
		return Database{}, err
	}
	row, err := new(sqlc.Queries).FindManagedPostgresPreviewDatabase(ctx, s.catalog, params)
	return databaseFromSQL(row), mapPostgresError(err)
}

// ReservePreview implements PreviewDatabaseStore.
func (s *PostgresStore) ReservePreview(ctx context.Context, database Database, limit int) (Database, bool, error) {
	owner := database.Preview
	plain := database
	plain.Preview = PreviewOwner{}
	if err := validateReservation(plain, 1); err != nil || limit < 0 || database.RestoreSourceDatabaseID != "" {
		return Database{}, false, ErrInvalid
	}
	params, err := previewOwnerParams(owner)
	if err != nil {
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
	tx, err := s.catalog.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Database{}, false, fmt.Errorf("managed postgres: begin preview reservation: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	q := new(sqlc.Queries)
	if err := s.lockReservationOwner(ctx, tx, database); err != nil {
		return Database{}, false, mapPostgresError(err)
	}
	row, err := q.FindManagedPostgresPreviewDatabase(ctx, tx, params)
	if err == nil {
		if err := tx.Commit(ctx); err != nil {
			return Database{}, false, mapPostgresError(err)
		}
		return databaseFromSQL(row), false, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Database{}, false, mapPostgresError(err)
	}
	_, err = q.FindManagedPostgresLifecycleDatabase(ctx, tx, sqlc.FindManagedPostgresLifecycleDatabaseParams{AccountID: accountID, Name: database.Name, OwnerOrgID: databaseNullableUUID(Organization(ctx))})
	if err == nil {
		return Database{}, false, fmt.Errorf("%w: a database named %q already exists", ErrConflict, database.Name)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Database{}, false, mapPostgresError(err)
	}
	if _, err := q.LockManagedPostgresCustomerDatabase(ctx, tx, sqlc.LockManagedPostgresCustomerDatabaseParams{AccountID: accountID, ID: params.SourceDatabaseID}); err != nil {
		return Database{}, false, mapPostgresError(err)
	}
	sourceRow, err := q.ReadManagedPostgresLifecycleRestoreSource(ctx, tx, params.SourceDatabaseID)
	if err != nil {
		return Database{}, false, mapPostgresError(err)
	}
	if err := previewSourceAllows(databaseFromSQL(sourceRow), database.AccountID); err != nil {
		return Database{}, false, err
	}
	held, err := s.countPreviewOwner(ctx, tx, database)
	if err != nil {
		return Database{}, false, mapPostgresError(err)
	}
	if held >= int64(limit) {
		return Database{}, false, ErrPreviewDatabaseLimit
	}
	if _, err := q.InsertManagedPostgresLifecycleDatabase(ctx, tx, lifecycleInsertParams(database, accountID, databaseID)); err != nil {
		return Database{}, false, mapPostgresError(err)
	}
	row, err = q.SetManagedPostgresPreviewOwner(ctx, tx, sqlc.SetManagedPostgresPreviewOwnerParams{
		SourceDatabaseID: params.SourceDatabaseID, EnvironmentID: params.EnvironmentID, PrNumber: params.PrNumber, ID: databaseID})
	if err != nil {
		return Database{}, false, mapPostgresError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Database{}, false, mapPostgresError(err)
	}
	return databaseFromSQL(row), true, nil
}

// ListPreviewDatabases implements PreviewDatabaseStore.
func (s *PostgresStore) ListPreviewDatabases(ctx context.Context, accountID, environmentID string, prNumber int) ([]Database, error) {
	account, err := postgresUUID(accountID)
	if err != nil {
		return nil, err
	}
	rows, err := new(sqlc.Queries).ListManagedPostgresPreviewDatabases(ctx, s.catalog, sqlc.ListManagedPostgresPreviewDatabasesParams{
		AccountID: account, EnvironmentID: environmentID, PrNumber: int32(prNumber)})
	if err != nil {
		return nil, mapPostgresError(err)
	}
	databases := make([]Database, 0, len(rows))
	for _, row := range rows {
		databases = append(databases, databaseFromSQL(row))
	}
	return databases, nil
}

// ListPreviewDatabasePage implements PreviewDatabaseStore.
func (s *PostgresStore) ListPreviewDatabasePage(ctx context.Context, afterID string, limit int) ([]Database, error) {
	after := pgtype.UUID{Valid: true}
	if afterID != "" {
		var err error
		if after, err = postgresUUID(afterID); err != nil {
			return nil, err
		}
	}
	rows, err := new(sqlc.Queries).ListManagedPostgresPreviewDatabasePage(ctx, s.catalog, sqlc.ListManagedPostgresPreviewDatabasePageParams{
		AfterID: after, PageSize: int32(min(limit, math.MaxInt32))})
	if err != nil {
		return nil, mapPostgresError(err)
	}
	databases := make([]Database, 0, len(rows))
	for _, row := range rows {
		databases = append(databases, databaseFromSQL(row))
	}
	return databases, nil
}

// countPreviewOwner counts the reservation owner's preview databases under
// its reservation lock, as countReservationOwner counts its databases.
func (s *PostgresStore) countPreviewOwner(ctx context.Context, tx sqlc.DBTX, database Database) (int64, error) {
	q := sqlc.New()
	owner, err := q.LockManagedPostgresReservationOwner(ctx, tx, sqlc.LockManagedPostgresReservationOwnerParams{OrgID: databaseNullableUUID(database.OrgID), AccountID: databaseNullableUUID(database.AccountID)})
	if err != nil {
		return 0, err
	}
	params := sqlc.CountManagedPostgresPreviewDatabasesParams{AccountID: databaseNullableUUID(database.AccountID)}
	if !owner.PersonalOrg && owner.ResourceOwnershipEnabled {
		params.OrgID = owner.ID
	}
	return q.CountManagedPostgresPreviewDatabases(ctx, tx, params)
}

func previewOwnerParams(owner PreviewOwner) (sqlc.FindManagedPostgresPreviewDatabaseParams, error) {
	if !owner.valid() {
		return sqlc.FindManagedPostgresPreviewDatabaseParams{}, ErrInvalid
	}
	source, err := postgresUUID(owner.SourceDatabaseID)
	if err != nil {
		return sqlc.FindManagedPostgresPreviewDatabaseParams{}, err
	}
	return sqlc.FindManagedPostgresPreviewDatabaseParams{SourceDatabaseID: source, EnvironmentID: owner.EnvironmentID, PrNumber: int32(owner.PRNumber)}, nil
}

// previewSourceAllows is the reservation's check of the source, read under
// the owner's reservation lock: the account's own database, not a preview
// database, not being deleted, with previews on.
func previewSourceAllows(source Database, accountID string) error {
	switch {
	case source.AccountID != accountID:
		return ErrNotFound
	case !source.Preview.IsZero():
		return ErrPreviewDatabase
	case source.State == StateDeleting || source.State == StateDeleted:
		return ErrConflict
	case source.Previews == "" || source.Previews == PreviewsOff:
		return ErrPreviewsOff
	}
	return nil
}
