package managedpostgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

const postgresBindingColumns = `id::text, account_id::text, database_id::text,
app_id::text, scope, environment_key, access, provider_identity_id,
credential_ref, credential_generation, state, last_error_code, lease_token,
lease_until, attempt_count, retry_at, created_at, updated_at, deleted_at`

var _ BindingStore = (*PostgresStore)(nil)

func (s *PostgresStore) ReserveBinding(ctx context.Context, binding Binding) (Binding, bool, error) {
	if err := validateBindingReservation(binding); err != nil {
		return Binding{}, false, err
	}
	id, err := postgresUUID(binding.ID)
	if err != nil {
		return Binding{}, false, err
	}
	accountID, err := postgresUUID(binding.AccountID)
	if err != nil {
		return Binding{}, false, err
	}
	databaseID, err := postgresUUID(binding.DatabaseID)
	if err != nil {
		return Binding{}, false, err
	}
	appID, err := postgresUUID(binding.AppID)
	if err != nil {
		return Binding{}, false, err
	}
	if binding.RetryAt.IsZero() {
		binding.RetryAt = binding.CreatedAt
	}

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Binding{}, false, fmt.Errorf("managed postgres: begin binding reservation: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	// This lock is shared with customer app-secret mutations. It turns the
	// cross-table "binding or customer secret, never both" rule into one
	// serial decision even when multiple apid replicas race.
	if _, err := tx.Exec(ctx,
		`SELECT pg_advisory_xact_lock(managed_secret_target_lock_key($1, $2, $3))`,
		appID, binding.Scope, binding.EnvironmentKey,
	); err != nil {
		return Binding{}, false, mapPostgresError(err)
	}

	var accountStatus string
	if err := tx.QueryRow(ctx,
		`SELECT status FROM accounts WHERE id = $1 FOR KEY SHARE`,
		accountID,
	).Scan(&accountStatus); err != nil {
		return Binding{}, false, mapPostgresError(err)
	}
	if accountStatus == "deleted_pending" {
		return Binding{}, false, ErrConflict
	}

	var databaseAccountID, databaseState string
	if err := tx.QueryRow(ctx,
		`SELECT account_id::text, state FROM managed_postgres_databases WHERE id = $1 FOR KEY SHARE`,
		databaseID,
	).Scan(&databaseAccountID, &databaseState); err != nil {
		return Binding{}, false, mapPostgresError(err)
	}
	if databaseAccountID != binding.AccountID {
		return Binding{}, false, ErrNotFound
	}
	if State(databaseState) != StateReady {
		return Binding{}, false, ErrConflict
	}

	var appAccountID, appStatus string
	if err := tx.QueryRow(ctx,
		`SELECT account_id::text, status FROM apps WHERE id = $1 FOR KEY SHARE`,
		appID,
	).Scan(&appAccountID, &appStatus); err != nil {
		return Binding{}, false, mapPostgresError(err)
	}
	if appAccountID != binding.AccountID {
		return Binding{}, false, ErrNotFound
	}
	if appStatus == "deleted" {
		return Binding{}, false, ErrConflict
	}

	existing, err := queryBinding(ctx, tx,
		`SELECT `+postgresBindingColumns+` FROM managed_postgres_bindings WHERE id = $1`,
		id,
	)
	if err == nil {
		if !sameBindingReservation(existing, binding) {
			return Binding{}, false, ErrConflict
		}
		if err := tx.Commit(ctx); err != nil {
			return Binding{}, false, mapPostgresError(err)
		}
		return existing, false, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return Binding{}, false, err
	}

	existing, err = queryBinding(ctx, tx,
		`SELECT `+postgresBindingColumns+` FROM managed_postgres_bindings
		 WHERE app_id = $1 AND scope = $2 AND environment_key = $3 AND state <> 'deleted'`,
		appID, binding.Scope, binding.EnvironmentKey,
	)
	if err == nil {
		if existing.DatabaseID != binding.DatabaseID || existing.Access != binding.Access {
			return Binding{}, false, ErrConflict
		}
		if err := tx.Commit(ctx); err != nil {
			return Binding{}, false, mapPostgresError(err)
		}
		return existing, false, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return Binding{}, false, err
	}

	var secretExists bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS(
			SELECT 1 FROM app_secrets
			WHERE app_id = $1 AND scope = $2 AND key = $3
		)`,
		appID, binding.Scope, binding.EnvironmentKey,
	).Scan(&secretExists); err != nil {
		return Binding{}, false, mapPostgresError(err)
	}
	if secretExists {
		return Binding{}, false, ErrConflict
	}

	created, err := queryBinding(ctx, tx,
		`INSERT INTO managed_postgres_bindings (
			id, account_id, database_id, app_id, scope, environment_key, access,
			credential_generation, state, retry_at, created_at, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
		RETURNING `+postgresBindingColumns,
		id, accountID, databaseID, appID, binding.Scope, binding.EnvironmentKey,
		string(binding.Access), binding.CredentialGeneration, string(binding.State),
		binding.RetryAt, binding.CreatedAt, binding.UpdatedAt,
	)
	if err != nil {
		return Binding{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Binding{}, false, mapPostgresError(err)
	}
	return created, true, nil
}

func (s *PostgresStore) GetBinding(ctx context.Context, accountID, bindingID string) (Binding, error) {
	account, err := postgresUUID(accountID)
	if err != nil {
		return Binding{}, err
	}
	id, err := postgresUUID(bindingID)
	if err != nil {
		return Binding{}, err
	}
	return queryBinding(ctx, s.pool,
		`SELECT `+postgresBindingColumns+` FROM managed_postgres_bindings
		 WHERE account_id = $1 AND id = $2`,
		account, id,
	)
}

func (s *PostgresStore) ListBindings(ctx context.Context, accountID, databaseID string) ([]Binding, error) {
	account, err := postgresUUID(accountID)
	if err != nil {
		return nil, err
	}
	database, err := postgresUUID(databaseID)
	if err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx,
		`SELECT `+postgresBindingColumns+` FROM managed_postgres_bindings
		 WHERE account_id = $1 AND database_id = $2 AND state <> 'deleted'
		 ORDER BY created_at, id`,
		account, database,
	)
	if err != nil {
		return nil, mapPostgresError(err)
	}
	defer rows.Close()
	items := make([]Binding, 0)
	for rows.Next() {
		binding, scanErr := scanBinding(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, binding)
	}
	if err := rows.Err(); err != nil {
		return nil, mapPostgresError(err)
	}
	return items, nil
}

func (s *PostgresStore) DueBindings(ctx context.Context, includeProvisioning bool, limit int, now time.Time) ([]Binding, error) {
	if limit < 1 || limit > 100 || now.IsZero() {
		return nil, ErrInvalid
	}
	rows, err := s.pool.Query(ctx,
		`SELECT `+postgresBindingColumns+` FROM managed_postgres_bindings
		 WHERE (state = 'deleting' OR ($1 AND state IN ('provisioning','failed')))
		   AND retry_at <= $2 AND (lease_until IS NULL OR lease_until <= $2)
		 ORDER BY retry_at, id LIMIT $3`,
		includeProvisioning, now, limit,
	)
	if err != nil {
		return nil, mapPostgresError(err)
	}
	defer rows.Close()
	items := make([]Binding, 0)
	for rows.Next() {
		binding, scanErr := scanBinding(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, binding)
	}
	if err := rows.Err(); err != nil {
		return nil, mapPostgresError(err)
	}
	return items, nil
}

func (s *PostgresStore) ClaimBinding(ctx context.Context, accountID, bindingID, leaseToken string, operation BindingState, now, leaseUntil time.Time) (Binding, error) {
	if leaseToken == "" || now.IsZero() || !leaseUntil.After(now) ||
		(operation != BindingStateProvisioning && operation != BindingStateDeleting) {
		return Binding{}, ErrInvalid
	}
	account, err := postgresUUID(accountID)
	if err != nil {
		return Binding{}, err
	}
	id, err := postgresUUID(bindingID)
	if err != nil {
		return Binding{}, err
	}
	binding, err := queryBinding(ctx, s.pool,
		`UPDATE managed_postgres_bindings SET
			state = $1, lease_token = $2, lease_until = $3, updated_at = $4,
			attempt_count = CASE WHEN $1 = 'deleting' AND state <> 'deleting'
				THEN 1 ELSE least(attempt_count + 1, 30) END,
			last_error_code = CASE WHEN state <> $1 THEN NULL ELSE last_error_code END,
			retry_at = $4
		 WHERE account_id = $5 AND id = $6 AND state <> 'deleted'
		   AND (lease_until IS NULL OR lease_until <= $4)
		   AND (($1 = 'provisioning' AND state IN ('provisioning','failed') AND retry_at <= $4)
		     OR ($1 = 'deleting'))
		 RETURNING `+postgresBindingColumns,
		string(operation), leaseToken, leaseUntil, now, account, id,
	)
	if !errors.Is(err, ErrNotFound) {
		return binding, err
	}
	var exists bool
	if existsErr := s.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM managed_postgres_bindings WHERE account_id = $1 AND id = $2)`,
		account, id,
	).Scan(&exists); existsErr != nil {
		return Binding{}, mapPostgresError(existsErr)
	}
	if !exists {
		return Binding{}, ErrNotFound
	}
	return Binding{}, ErrConflict
}

func (s *PostgresStore) FinishBindingProvision(ctx context.Context, bindingID, leaseToken, providerIdentityID, credentialRef string, now time.Time) (Binding, error) {
	if leaseToken == "" || !validOpaqueID(providerIdentityID) || !validOpaqueID(credentialRef) || now.IsZero() {
		return Binding{}, ErrInvalid
	}
	id, err := postgresUUID(bindingID)
	if err != nil {
		return Binding{}, err
	}
	binding, err := queryBinding(ctx, s.pool,
		`UPDATE managed_postgres_bindings AS binding SET state = 'ready',
			provider_identity_id = $1, credential_ref = $2,
			last_error_code = NULL, lease_token = NULL, lease_until = NULL,
			attempt_count = 0, retry_at = $3, updated_at = $3
		 WHERE binding.id = $4 AND binding.state = 'provisioning' AND binding.lease_token = $5
		   AND binding.lease_until > $3
		   AND EXISTS (
			SELECT 1 FROM app_secrets secret
			WHERE secret.managed_postgres_binding_id = binding.id
			  AND secret.managed_credential_ref = $2
			  AND secret.managed_credential_generation = binding.credential_generation
		   )
		 RETURNING `+postgresBindingColumns,
		providerIdentityID, credentialRef, now, id, leaseToken,
	)
	if errors.Is(err, ErrNotFound) {
		return Binding{}, ErrConflict
	}
	return binding, err
}

func (s *PostgresStore) ReleaseBinding(ctx context.Context, bindingID, leaseToken string, next BindingState, errorCode string, now, retryAt time.Time) error {
	if leaseToken == "" || now.IsZero() || retryAt.Before(now) || !validErrorCode(errorCode) ||
		(next != BindingStateProvisioning && next != BindingStateDeleting && next != BindingStateFailed) {
		return ErrInvalid
	}
	id, err := postgresUUID(bindingID)
	if err != nil {
		return err
	}
	command, err := s.pool.Exec(ctx,
		`UPDATE managed_postgres_bindings SET state = $1, last_error_code = NULLIF($2, ''),
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

func (s *PostgresStore) FinishBindingDelete(ctx context.Context, bindingID, leaseToken string, now time.Time) (Binding, error) {
	if leaseToken == "" || now.IsZero() {
		return Binding{}, ErrInvalid
	}
	id, err := postgresUUID(bindingID)
	if err != nil {
		return Binding{}, err
	}
	binding, err := queryBinding(ctx, s.pool,
		`UPDATE managed_postgres_bindings AS binding SET state = 'deleted',
			last_error_code = NULL, lease_token = NULL, lease_until = NULL,
			attempt_count = 0, retry_at = $1, updated_at = $1, deleted_at = $1
		 WHERE binding.id = $2 AND binding.state = 'deleting' AND binding.lease_token = $3
		   AND binding.lease_until > $1
		   AND NOT EXISTS (
			SELECT 1 FROM app_secrets secret
			WHERE secret.managed_postgres_binding_id = binding.id
		   )
		 RETURNING `+postgresBindingColumns,
		now, id, leaseToken,
	)
	if errors.Is(err, ErrNotFound) {
		return Binding{}, ErrConflict
	}
	return binding, err
}

type bindingScanner interface {
	Scan(...any) error
}

func queryBinding(ctx context.Context, queryer databaseQueryer, query string, arguments ...any) (Binding, error) {
	return scanBinding(queryer.QueryRow(ctx, query, arguments...))
}

func scanBinding(row bindingScanner) (Binding, error) {
	var binding Binding
	var providerIdentityID, credentialRef, lastErrorCode, leaseToken pgtype.Text
	var leaseUntil, deletedAt pgtype.Timestamptz
	if err := row.Scan(
		&binding.ID, &binding.AccountID, &binding.DatabaseID, &binding.AppID,
		&binding.Scope, &binding.EnvironmentKey, &binding.Access,
		&providerIdentityID, &credentialRef, &binding.CredentialGeneration,
		&binding.State, &lastErrorCode, &leaseToken, &leaseUntil,
		&binding.AttemptCount, &binding.RetryAt, &binding.CreatedAt,
		&binding.UpdatedAt, &deletedAt,
	); err != nil {
		return Binding{}, mapPostgresError(err)
	}
	if providerIdentityID.Valid {
		binding.ProviderIdentityID = providerIdentityID.String
	}
	if credentialRef.Valid {
		binding.CredentialRef = credentialRef.String
	}
	if lastErrorCode.Valid {
		binding.LastErrorCode = lastErrorCode.String
	}
	if leaseToken.Valid {
		binding.LeaseToken = leaseToken.String
	}
	if leaseUntil.Valid {
		binding.LeaseUntil = leaseUntil.Time
	}
	if deletedAt.Valid {
		binding.DeletedAt = &deletedAt.Time
	}
	return binding, nil
}
