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
)

const postgresDatabaseColumns = `id::text, account_id::text, name, region,
postgres_major, service_class, availability, scale_to_zero,
storage_limit_bytes, restore_window_seconds, backend_id,
backend_fingerprint, provider_resource_id, state, desired_generation,
observed_generation, last_error_code, lease_token, lease_until,
attempt_count, retry_at, created_at, updated_at, deleted_at`

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

	// The account row is the per-tenant serialization point. It makes the
	// quota check atomic across replicas and races safely with account deletion.
	var locked string
	if err := tx.QueryRow(ctx,
		`SELECT id::text FROM accounts WHERE id = $1 AND status <> 'deleted_pending' FOR UPDATE`,
		accountID,
	).Scan(&locked); err != nil {
		return Database{}, false, mapPostgresError(err)
	}

	existing, err := queryDatabase(ctx, tx,
		`SELECT `+postgresDatabaseColumns+` FROM managed_postgres_databases
		 WHERE account_id = $1 AND name = $2 AND state <> 'deleted'`,
		accountID, database.Name,
	)
	if err == nil {
		if err := tx.Commit(ctx); err != nil {
			return Database{}, false, mapPostgresError(err)
		}
		return existing, false, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return Database{}, false, err
	}

	var active int
	if err := tx.QueryRow(ctx,
		`SELECT count(*) FROM managed_postgres_databases WHERE account_id = $1 AND state <> 'deleted'`,
		accountID,
	).Scan(&active); err != nil {
		return Database{}, false, mapPostgresError(err)
	}
	if active >= limit {
		return Database{}, false, ErrQuotaExceeded
	}

	created, err := queryDatabase(ctx, tx,
		`INSERT INTO managed_postgres_databases (
			id, account_id, name, region, postgres_major, service_class,
			availability, scale_to_zero, storage_limit_bytes,
			restore_window_seconds, backend_id, backend_fingerprint, state,
			desired_generation, observed_generation, retry_at, created_at, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)
		RETURNING `+postgresDatabaseColumns,
		databaseID, accountID, database.Name, database.Spec.Region,
		database.Spec.PostgresMajor, string(database.Spec.Class), string(database.Spec.Availability),
		database.Spec.ScaleToZero, database.Spec.StorageLimitBytes,
		database.Spec.RestoreWindowSeconds, database.BackendID,
		database.BackendFingerprint, string(database.State), database.DesiredGeneration,
		database.ObservedGeneration, database.RetryAt, database.CreatedAt, database.UpdatedAt,
	)
	if err != nil {
		return Database{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Database{}, false, mapPostgresError(err)
	}
	return created, true, nil
}

func (s *PostgresStore) FindByName(ctx context.Context, accountID, name string) (Database, error) {
	account, err := postgresUUID(accountID)
	if err != nil || !ValidName(name) {
		return Database{}, ErrInvalid
	}
	return queryDatabase(ctx, s.pool,
		`SELECT `+postgresDatabaseColumns+` FROM managed_postgres_databases
		 WHERE account_id = $1 AND name = $2 AND state <> 'deleted'`,
		account, name,
	)
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
	return queryDatabase(ctx, s.pool,
		`SELECT `+postgresDatabaseColumns+` FROM managed_postgres_databases
		 WHERE account_id = $1 AND id = $2`,
		account, id,
	)
}

func (s *PostgresStore) List(ctx context.Context, accountID string) ([]Database, error) {
	account, err := postgresUUID(accountID)
	if err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx,
		`SELECT `+postgresDatabaseColumns+` FROM managed_postgres_databases
		 WHERE account_id = $1 AND state <> 'deleted' ORDER BY created_at, id`,
		account,
	)
	if err != nil {
		return nil, mapPostgresError(err)
	}
	defer rows.Close()
	items := make([]Database, 0)
	for rows.Next() {
		database, scanErr := scanDatabase(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, database)
	}
	if err := rows.Err(); err != nil {
		return nil, mapPostgresError(err)
	}
	return items, nil
}

func (s *PostgresStore) Due(ctx context.Context, includeProvisioning bool, limit int, now time.Time) ([]Database, error) {
	if limit < 1 || limit > 100 || now.IsZero() {
		return nil, ErrInvalid
	}
	rows, err := s.pool.Query(ctx,
		`SELECT `+postgresDatabaseColumns+` FROM managed_postgres_databases
		 WHERE (state = 'deleting' OR ($1 AND state IN ('provisioning','failed')))
		   AND retry_at <= $2 AND (lease_until IS NULL OR lease_until <= $2)
		 ORDER BY retry_at, id LIMIT $3`,
		includeProvisioning, now, limit,
	)
	if err != nil {
		return nil, mapPostgresError(err)
	}
	defer rows.Close()
	items := make([]Database, 0)
	for rows.Next() {
		database, scanErr := scanDatabase(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, database)
	}
	if err := rows.Err(); err != nil {
		return nil, mapPostgresError(err)
	}
	return items, nil
}

func (s *PostgresStore) Claim(ctx context.Context, accountID, databaseID, leaseToken string, operation State, now, leaseUntil time.Time) (Database, error) {
	if leaseToken == "" || now.IsZero() || !leaseUntil.After(now) || (operation != StateProvisioning && operation != StateDeleting) {
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
	database, err := queryDatabase(ctx, s.pool,
		`UPDATE managed_postgres_databases SET
			state = $1, lease_token = $2, lease_until = $3, updated_at = $4,
			attempt_count = CASE WHEN $1 = 'deleting' AND state <> 'deleting'
				THEN 1 ELSE least(attempt_count + 1, 30) END,
			last_error_code = CASE WHEN state <> $1 THEN NULL ELSE last_error_code END,
			retry_at = $4
		 WHERE account_id = $5 AND id = $6 AND state <> 'deleted'
		   AND (lease_until IS NULL OR lease_until <= $4)
		   AND (($1 = 'provisioning' AND state IN ('provisioning','failed') AND retry_at <= $4)
		     OR ($1 = 'deleting'))
		 RETURNING `+postgresDatabaseColumns,
		string(operation), leaseToken, leaseUntil, now, account, id,
	)
	if !errors.Is(err, ErrNotFound) {
		return database, err
	}
	var exists bool
	if existsErr := s.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM managed_postgres_databases WHERE account_id = $1 AND id = $2)`,
		account, id,
	).Scan(&exists); existsErr != nil {
		return Database{}, mapPostgresError(existsErr)
	}
	if !exists {
		return Database{}, ErrNotFound
	}
	return Database{}, ErrConflict
}

func (s *PostgresStore) RecordProviderResource(ctx context.Context, databaseID, leaseToken, providerResourceID string, now time.Time) error {
	if leaseToken == "" || providerResourceID == "" || now.IsZero() {
		return ErrInvalid
	}
	id, err := postgresUUID(databaseID)
	if err != nil {
		return err
	}
	command, err := s.pool.Exec(ctx,
		`UPDATE managed_postgres_databases SET provider_resource_id = $1, updated_at = $2
		 WHERE id = $3 AND state = 'provisioning' AND lease_token = $4
		   AND lease_until > $2
		   AND (provider_resource_id IS NULL OR provider_resource_id = $1)`,
		providerResourceID, now, id, leaseToken,
	)
	if err != nil {
		return mapPostgresError(err)
	}
	if command.RowsAffected() != 1 {
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
	database, err := queryDatabase(ctx, s.pool,
		`UPDATE managed_postgres_databases SET state = 'ready',
			observed_generation = desired_generation, last_error_code = NULL,
			lease_token = NULL, lease_until = NULL, attempt_count = 0,
			retry_at = $1, updated_at = $1
		 WHERE id = $2 AND state = 'provisioning' AND lease_token = $3
		   AND lease_until > $1
		   AND provider_resource_id IS NOT NULL
		 RETURNING `+postgresDatabaseColumns,
		now, id, leaseToken,
	)
	if errors.Is(err, ErrNotFound) {
		return Database{}, ErrConflict
	}
	return database, err
}

func (s *PostgresStore) Release(ctx context.Context, databaseID, leaseToken string, next State, errorCode string, now, retryAt time.Time) error {
	if leaseToken == "" || now.IsZero() || retryAt.Before(now) || !validErrorCode(errorCode) || (next != StateProvisioning && next != StateDeleting && next != StateFailed) {
		return ErrInvalid
	}
	id, err := postgresUUID(databaseID)
	if err != nil {
		return err
	}
	command, err := s.pool.Exec(ctx,
		`UPDATE managed_postgres_databases SET state = $1, last_error_code = NULLIF($2, ''),
			lease_token = NULL, lease_until = NULL, retry_at = $3, updated_at = $4
		 WHERE id = $5 AND lease_token = $6 AND lease_until > $4`,
		string(next), errorCode, retryAt, now, id, leaseToken,
	)
	if err != nil {
		return mapPostgresError(err)
	}
	if command.RowsAffected() != 1 {
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
	database, err := queryDatabase(ctx, s.pool,
		`UPDATE managed_postgres_databases SET state = 'deleted',
			last_error_code = NULL, lease_token = NULL, lease_until = NULL,
			attempt_count = 0, retry_at = $1, updated_at = $1, deleted_at = $1
		 WHERE id = $2 AND state = 'deleting' AND lease_token = $3
		   AND lease_until > $1
		 RETURNING `+postgresDatabaseColumns,
		now, id, leaseToken,
	)
	if errors.Is(err, ErrNotFound) {
		return Database{}, ErrConflict
	}
	return database, err
}

type databaseScanner interface {
	Scan(...any) error
}

type databaseQueryer interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func queryDatabase(ctx context.Context, queryer databaseQueryer, query string, arguments ...any) (Database, error) {
	return scanDatabase(queryer.QueryRow(ctx, query, arguments...))
}

func scanDatabase(row databaseScanner) (Database, error) {
	var database Database
	var providerResourceID, lastErrorCode, leaseToken pgtype.Text
	var leaseUntil, deletedAt pgtype.Timestamptz
	if err := row.Scan(
		&database.ID, &database.AccountID, &database.Name, &database.Spec.Region,
		&database.Spec.PostgresMajor, &database.Spec.Class, &database.Spec.Availability,
		&database.Spec.ScaleToZero, &database.Spec.StorageLimitBytes,
		&database.Spec.RestoreWindowSeconds, &database.BackendID,
		&database.BackendFingerprint, &providerResourceID, &database.State,
		&database.DesiredGeneration, &database.ObservedGeneration, &lastErrorCode,
		&leaseToken, &leaseUntil, &database.AttemptCount, &database.RetryAt,
		&database.CreatedAt, &database.UpdatedAt, &deletedAt,
	); err != nil {
		return Database{}, mapPostgresError(err)
	}
	if providerResourceID.Valid {
		database.ProviderResourceID = providerResourceID.String
	}
	if lastErrorCode.Valid {
		database.LastErrorCode = lastErrorCode.String
	}
	if leaseToken.Valid {
		database.LeaseToken = leaseToken.String
	}
	if leaseUntil.Valid {
		database.LeaseUntil = leaseUntil.Time
	}
	if deletedAt.Valid {
		database.DeletedAt = &deletedAt.Time
	}
	return database, nil
}

func validateReservation(database Database, limit int) error {
	validFingerprint := regexp.MustCompile(`^[a-f0-9]{64}$`)
	if limit < 1 || limit > 100 || database.ID == "" || database.AccountID == "" ||
		!ValidName(database.Name) || database.Spec.Validate() != nil ||
		database.State != StateProvisioning || !ValidName(database.BackendID) ||
		!validFingerprint.MatchString(database.BackendFingerprint) ||
		database.DesiredGeneration < 1 || database.ObservedGeneration != 0 ||
		database.CreatedAt.IsZero() || database.UpdatedAt.IsZero() ||
		database.ProviderResourceID != "" || database.LeaseToken != "" || database.DeletedAt != nil {
		return ErrInvalid
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
	case pgerrcode.UniqueViolation:
		return fmt.Errorf("%w: %s", ErrConflict, postgresError.ConstraintName)
	case pgerrcode.ForeignKeyViolation:
		return ErrNotFound
	case pgerrcode.CheckViolation:
		if postgresError.ConstraintName == "managed_postgres_database_has_bindings" {
			return ErrConflict
		}
		return fmt.Errorf("%w: %s", ErrInvalid, postgresError.ConstraintName)
	default:
		return err
	}
}
