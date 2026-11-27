package managedpostgres

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ CustomRoleStore = (*PostgresStore)(nil)

// customRoleFenceSQL serializes custom-role declarations with Data API and
// Auth binding reservations of one database (ADR-20261116213000).
const customRoleFenceSQL = `SELECT pg_advisory_xact_lock(hashtextextended('managed_postgres_custom_roles:' || $1::text, 0))`

const customRoleColumns = `account_id, database_id, name, bypass_rls, granted_to, member_of, default_for, state, revision, last_error, attempt_count, retry_at, created_at, updated_at`

func scanCustomRole(row pgx.Row) (CustomRole, error) {
	var (
		role                  CustomRole
		accountID, databaseID pgtype.UUID
		grants, defaults      []string
		state                 string
	)
	if err := row.Scan(&accountID, &databaseID, &role.Name, &role.BypassRLS, &grants, &role.MemberOf, &defaults, &state, &role.Revision,
		&role.LastError, &role.AttemptCount, &role.RetryAt, &role.CreatedAt, &role.UpdatedAt); err != nil {
		return CustomRole{}, err
	}
	role.AccountID, role.DatabaseID, role.State = databaseUUID(accountID), databaseUUID(databaseID), CustomRoleState(state)
	for _, grant := range grants {
		role.GrantedTo = append(role.GrantedTo, CredentialAccess(grant))
	}
	if role.MemberOf == nil {
		role.MemberOf = []string{}
	}
	for _, access := range defaults {
		role.DefaultFor = append(role.DefaultFor, CredentialAccess(access))
	}
	role.RetryAt, role.CreatedAt, role.UpdatedAt = role.RetryAt.UTC(), role.CreatedAt.UTC(), role.UpdatedAt.UTC()
	return role, nil
}

func customRoleIDs(accountID, databaseID string) (pgtype.UUID, pgtype.UUID, error) {
	account, err := postgresUUID(accountID)
	if err != nil {
		return pgtype.UUID{}, pgtype.UUID{}, err
	}
	database, err := postgresUUID(databaseID)
	if err != nil {
		return pgtype.UUID{}, pgtype.UUID{}, err
	}
	return account, database, nil
}

// lockCustomRoleDatabase takes the declaration fence and checks the database
// is a published customer database of the account.
func lockCustomRoleDatabase(ctx context.Context, tx pgx.Tx, account, database pgtype.UUID) (string, error) {
	if _, err := tx.Exec(ctx, customRoleFenceSQL, database); err != nil {
		return "", mapPostgresError(err)
	}
	if _, err := sqlc.New().LockManagedPostgresCustomerDatabase(ctx, tx, sqlc.LockManagedPostgresCustomerDatabaseParams{AccountID: account, ID: database}); err != nil {
		return "", mapPostgresError(err)
	}
	var state string
	if err := tx.QueryRow(ctx, `SELECT state FROM managed_postgres_databases WHERE id = $1`, database).Scan(&state); err != nil {
		return "", mapPostgresError(err)
	}
	return state, nil
}

func (s *PostgresStore) ListCustomRoles(ctx context.Context, accountID, databaseID string) ([]CustomRole, error) {
	account, database, err := customRoleIDs(accountID, databaseID)
	if err != nil {
		return nil, err
	}
	var visible bool
	if err := s.catalog.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM managed_postgres_databases WHERE id = $1 AND account_id = $2 AND environment_clone_operation_id IS NULL)`,
		database, account).Scan(&visible); err != nil {
		return nil, mapPostgresError(err)
	}
	if !visible {
		return nil, ErrNotFound
	}
	rows, err := s.catalog.Query(ctx, `SELECT `+customRoleColumns+` FROM managed_postgres_custom_roles WHERE database_id = $1 AND account_id = $2 ORDER BY name`, database, account)
	if err != nil {
		return nil, mapPostgresError(err)
	}
	defer rows.Close()
	var out []CustomRole
	for rows.Next() {
		role, err := scanCustomRole(rows)
		if err != nil {
			return nil, mapPostgresError(err)
		}
		out = append(out, role)
	}
	return out, mapPostgresError(rows.Err())
}

func (s *PostgresStore) SaveCustomRole(ctx context.Context, role CustomRole, limit int, now time.Time) (CustomRole, error) {
	account, database, err := customRoleIDs(role.AccountID, role.DatabaseID)
	if err != nil {
		return CustomRole{}, err
	}
	tx, err := s.catalog.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return CustomRole{}, fmt.Errorf("managed postgres: begin custom role save: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	state, err := lockCustomRoleDatabase(ctx, tx, account, database)
	if err != nil {
		return CustomRole{}, err
	}
	if state != string(StateReady) {
		return CustomRole{}, ErrConflict
	}
	rows, err := tx.Query(ctx, `SELECT `+customRoleColumns+` FROM managed_postgres_custom_roles WHERE database_id = $1 FOR UPDATE`, database)
	if err != nil {
		return CustomRole{}, mapPostgresError(err)
	}
	var current []CustomRole
	for rows.Next() {
		existing, err := scanCustomRole(rows)
		if err != nil {
			rows.Close()
			return CustomRole{}, mapPostgresError(err)
		}
		current = append(current, existing)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return CustomRole{}, mapPostgresError(err)
	}
	var existing *CustomRole
	byName := map[string]CustomRole{}
	for i := range current {
		byName[current[i].Name] = current[i]
		if current[i].Name == role.Name {
			existing = &current[i]
		}
	}
	if existing != nil && existing.State == CustomRoleDeleting {
		return CustomRole{}, ErrConflict
	}
	if existing == nil && limit > 0 && len(current) >= limit {
		return CustomRole{}, ErrQuotaExceeded
	}
	for _, parent := range role.MemberOf {
		other, ok := byName[parent]
		if !ok || other.State == CustomRoleDeleting || parent == role.Name {
			return CustomRole{}, ErrInvalid
		}
	}
	if customRoleCycle(current, role.Name, role.MemberOf) {
		return CustomRole{}, ErrInvalid
	}
	if customRoleDefaultTaken(current, role) != "" {
		return CustomRole{}, ErrCustomRoleDefaultTaken
	}
	if role.BypassRLS {
		var fenced bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM managed_postgres_bindings
 WHERE database_id = $1 AND access IN ('data_api', 'auth') AND state <> 'deleted')`, database).Scan(&fenced); err != nil {
			return CustomRole{}, mapPostgresError(err)
		}
		if fenced {
			return CustomRole{}, ErrBypassRLSConflict
		}
	}
	grants := make([]string, 0, len(role.GrantedTo))
	for _, grant := range role.GrantedTo {
		grants = append(grants, string(grant))
	}
	memberOf := slices.Clone(role.MemberOf)
	if memberOf == nil {
		memberOf = []string{}
	}
	defaults := make([]string, 0, len(role.DefaultFor))
	for _, access := range role.DefaultFor {
		defaults = append(defaults, string(access))
	}
	saved, err := scanCustomRole(tx.QueryRow(ctx, `INSERT INTO managed_postgres_custom_roles
 (database_id, account_id, name, bypass_rls, granted_to, member_of, default_for, state, revision, last_error, attempt_count, retry_at, created_at, updated_at)
 VALUES ($1, $2, $3, $4, $5, $6, $8, 'pending', 1, '', 0, $7, $7, $7)
 ON CONFLICT (database_id, name) DO UPDATE SET bypass_rls = EXCLUDED.bypass_rls, granted_to = EXCLUDED.granted_to,
   member_of = EXCLUDED.member_of, default_for = EXCLUDED.default_for, state = 'pending', revision = managed_postgres_custom_roles.revision + 1,
   last_error = '', attempt_count = 0, retry_at = EXCLUDED.retry_at, updated_at = EXCLUDED.updated_at
 RETURNING `+customRoleColumns, database, account, role.Name, role.BypassRLS, grants, memberOf, now, defaults))
	if err != nil {
		return CustomRole{}, mapPostgresError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return CustomRole{}, mapPostgresError(err)
	}
	return saved, nil
}

func (s *PostgresStore) DeleteCustomRole(ctx context.Context, accountID, databaseID, name string, now time.Time) (CustomRole, error) {
	account, database, err := customRoleIDs(accountID, databaseID)
	if err != nil {
		return CustomRole{}, err
	}
	tx, err := s.catalog.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return CustomRole{}, fmt.Errorf("managed postgres: begin custom role delete: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := lockCustomRoleDatabase(ctx, tx, account, database); err != nil {
		return CustomRole{}, err
	}
	var parentOfOthers bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM managed_postgres_custom_roles
 WHERE database_id = $1 AND state <> 'deleting' AND $2 = ANY(member_of))`, database, name).Scan(&parentOfOthers); err != nil {
		return CustomRole{}, mapPostgresError(err)
	}
	if parentOfOthers {
		return CustomRole{}, ErrConflict
	}
	role, err := scanCustomRole(tx.QueryRow(ctx, `UPDATE managed_postgres_custom_roles SET
 state = 'deleting', revision = CASE WHEN state = 'deleting' THEN revision ELSE revision + 1 END,
 last_error = CASE WHEN state = 'deleting' THEN last_error ELSE '' END,
 attempt_count = CASE WHEN state = 'deleting' THEN attempt_count ELSE 0 END,
 retry_at = CASE WHEN state = 'deleting' THEN retry_at ELSE $4 END,
 updated_at = CASE WHEN state = 'deleting' THEN updated_at ELSE $4 END
 WHERE database_id = $1 AND account_id = $2 AND name = $3 RETURNING `+customRoleColumns, database, account, name, now))
	if errors.Is(err, pgx.ErrNoRows) {
		return CustomRole{}, ErrNotFound
	}
	if err != nil {
		return CustomRole{}, mapPostgresError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return CustomRole{}, mapPostgresError(err)
	}
	return role, nil
}

func (s *PostgresStore) FinishCustomRoleApply(ctx context.Context, accountID, databaseID string, applied map[string]int64, now time.Time) error {
	account, database, err := customRoleIDs(accountID, databaseID)
	if err != nil {
		return err
	}
	tx, err := s.catalog.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("managed postgres: begin custom role finish: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	for name, revision := range applied {
		if _, err := tx.Exec(ctx, `DELETE FROM managed_postgres_custom_roles
 WHERE database_id = $1 AND account_id = $2 AND name = $3 AND revision = $4 AND state = 'deleting'`, database, account, name, revision); err != nil {
			return mapPostgresError(err)
		}
		if _, err := tx.Exec(ctx, `UPDATE managed_postgres_custom_roles SET state = 'ready', last_error = '', attempt_count = 0, updated_at = $5
 WHERE database_id = $1 AND account_id = $2 AND name = $3 AND revision = $4 AND state = 'pending'`, database, account, name, revision, now); err != nil {
			return mapPostgresError(err)
		}
	}
	return mapPostgresError(tx.Commit(ctx))
}

func (s *PostgresStore) FailCustomRoleApply(ctx context.Context, accountID, databaseID, code string, retryAt, now time.Time) error {
	account, database, err := customRoleIDs(accountID, databaseID)
	if err != nil {
		return err
	}
	if len(code) > 128 {
		code = code[:128]
	}
	_, err = s.catalog.Exec(ctx, `UPDATE managed_postgres_custom_roles SET last_error = $3, retry_at = $4, updated_at = $5,
 attempt_count = attempt_count + 1 WHERE database_id = $1 AND account_id = $2 AND state <> 'ready'`, database, account, code, retryAt, now)
	return mapPostgresError(err)
}

func (s *PostgresStore) DueCustomRoleDatabases(ctx context.Context, limit int, now time.Time) ([]CustomRoleDatabase, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.catalog.Query(ctx, `SELECT account_id, database_id FROM managed_postgres_custom_roles
 WHERE state <> 'ready' AND retry_at <= $1 GROUP BY account_id, database_id ORDER BY min(retry_at) LIMIT $2`, now, limit)
	if err != nil {
		return nil, mapPostgresError(err)
	}
	defer rows.Close()
	var out []CustomRoleDatabase
	for rows.Next() {
		var account, database pgtype.UUID
		if err := rows.Scan(&account, &database); err != nil {
			return nil, mapPostgresError(err)
		}
		out = append(out, CustomRoleDatabase{AccountID: databaseUUID(account), DatabaseID: databaseUUID(database)})
	}
	return out, mapPostgresError(rows.Err())
}

func (s *PostgresStore) HasBypassRLSCustomRole(ctx context.Context, accountID, databaseID string) (bool, error) {
	account, database, err := customRoleIDs(accountID, databaseID)
	if err != nil {
		return false, err
	}
	var exists bool
	err = s.catalog.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM managed_postgres_custom_roles WHERE database_id = $1 AND account_id = $2 AND bypass_rls)`,
		database, account).Scan(&exists)
	return exists, mapPostgresError(err)
}

// customRoleBindingFence refuses a Data API or Auth reservation on a database
// with a declared BYPASSRLS role. It runs inside ReserveBinding's
// transaction, under the declaration fence.
func customRoleBindingFence(ctx context.Context, tx pgx.Tx, binding Binding, database pgtype.UUID) error {
	if binding.Access != CredentialDataAPI && binding.Access != CredentialAuth {
		return nil
	}
	if _, err := tx.Exec(ctx, customRoleFenceSQL, database); err != nil {
		return mapPostgresError(err)
	}
	var bypass bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM managed_postgres_custom_roles WHERE database_id = $1 AND bypass_rls)`, database).Scan(&bypass); err != nil {
		return mapPostgresError(err)
	}
	if bypass {
		return ErrBypassRLSConflict
	}
	return nil
}
