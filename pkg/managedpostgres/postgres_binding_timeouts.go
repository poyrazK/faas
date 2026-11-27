package managedpostgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func readBindingTimeouts(ctx context.Context, db sqlc.DBTX, id pgtype.UUID) (BindingTimeoutStatus, error) {
	status := BindingTimeoutStatus{BindingID: id.String()}
	row, err := sqlc.New().GetManagedPostgresBindingTimeoutPolicy(ctx, db, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return status, nil
	}
	if err != nil {
		return status, err
	}
	status.Revision = row.Revision
	status.StatementMS = row.StatementTimeoutMs
	status.LockMS = row.LockTimeoutMs
	status.IdleInTransactionMS = row.IdleInTransactionSessionTimeoutMs
	if row.AppliedAt.Valid {
		status.AppliedAt = &row.AppliedAt.Time
	}
	return status, nil
}

func (s *PostgresStore) bindingTimeouts(ctx context.Context, bindingID string) (BindingTimeoutStatus, error) {
	id, err := postgresUUID(bindingID)
	if err != nil {
		return BindingTimeoutStatus{}, err
	}
	return readBindingTimeouts(ctx, s.catalog, id)
}

func (s *PostgresStore) saveBindingTimeouts(ctx context.Context, binding Binding, patch BindingTimeoutPatch) (BindingTimeoutStatus, error) {
	id, err := postgresUUID(binding.ID)
	if err != nil {
		return BindingTimeoutStatus{}, err
	}
	account, err := postgresUUID(binding.AccountID)
	if err != nil {
		return BindingTimeoutStatus{}, err
	}
	tx, err := s.catalog.Begin(ctx)
	if err != nil {
		return BindingTimeoutStatus{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	q := sqlc.New()
	ready, err := q.LockManagedPostgresBindingTimeoutEdit(ctx, tx, sqlc.LockManagedPostgresBindingTimeoutEditParams{
		BindingID: id, AccountID: account,
	})
	if err != nil {
		return BindingTimeoutStatus{}, mapPostgresError(err)
	}
	if !ready.Bool {
		return BindingTimeoutStatus{}, ErrConflict
	}
	status, err := readBindingTimeouts(ctx, tx, id)
	if err != nil {
		return BindingTimeoutStatus{}, err
	}
	defaults := patch.apply(status.BindingTimeoutDefaults)
	if defaults != status.BindingTimeoutDefaults {
		status.BindingTimeoutDefaults = defaults
		status.AppliedAt = nil
		status.Revision, err = q.SaveManagedPostgresBindingTimeoutPolicy(ctx, tx, sqlc.SaveManagedPostgresBindingTimeoutPolicyParams{
			BindingID: id, StatementTimeoutMs: defaults.StatementMS, LockTimeoutMs: defaults.LockMS,
			IdleInTransactionSessionTimeoutMs: defaults.IdleInTransactionMS,
		})
		if err != nil {
			return BindingTimeoutStatus{}, err
		}
	}
	return status, tx.Commit(ctx)
}

func (s *PostgresStore) dueBindingTimeouts(ctx context.Context, limit int) ([]Binding, error) {
	// The binding reconciler validates the batch size to 1–100.
	rows, err := sqlc.New().DueManagedPostgresBindingTimeoutPolicies(ctx, s.catalog, int32(limit))
	if err != nil {
		return nil, err
	}
	bindings := make([]Binding, 0, len(rows))
	for _, row := range rows {
		bindings = append(bindings, bindingFromSQL(row))
	}
	return bindings, nil
}

func (s *PostgresStore) applyBindingTimeouts(ctx context.Context, binding Binding, resourceID string, provider BindingTimeoutProvider) error {
	id, err := postgresUUID(binding.ID)
	if err != nil {
		return err
	}
	resource, err := postgresUUID(resourceID)
	if err != nil {
		return err
	}
	tx, err := s.catalog.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	// Hold the binding row across the guest update so rotation, delete and
	// newer desired policies cannot advance while this role is changing.
	q := sqlc.New()
	role, err := q.LockManagedPostgresBindingTimeoutApply(ctx, tx, sqlc.LockManagedPostgresBindingTimeoutApplyParams{
		BindingID: id, CredentialGeneration: binding.CredentialGeneration,
	})
	if err != nil {
		return mapPostgresError(err)
	}
	status, err := readBindingTimeouts(ctx, tx, id)
	if err != nil || status.Revision == 0 || status.AppliedAt != nil {
		return err
	}
	if err := provider.ApplyBindingTimeouts(ctx, resourceID, role.String, status.BindingTimeoutPolicy); err != nil {
		// Use a fresh bounded context to record a retry after a guest deadline.
		finish, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cancel()
		recordErr := q.RetryManagedPostgresBindingTimeoutPolicy(finish, tx, id)
		if recordErr == nil {
			recordErr = tx.Commit(finish)
		}
		return errors.Join(err, recordErr)
	}
	published, err := q.PublishDatabaseRolePoolRevision(ctx, tx, sqlc.PublishDatabaseRolePoolRevisionParams{
		DatabaseID: resource, RoleName: role.String, Revision: status.Revision,
	})
	if err != nil {
		return err
	}
	if published != 1 {
		return ErrUnavailable
	}
	if err := q.ApplyManagedPostgresBindingTimeoutPolicy(ctx, tx, id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
