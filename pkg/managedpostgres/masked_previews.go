package managedpostgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres/maskedbranch"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var (
	ErrMaskedPreviewUnsupported      = fmt.Errorf("%w: masked PR previews require a personal native PostgreSQL database", ErrUnsupported)
	ErrPreviewReplacementRequired    = fmt.Errorf("%w: remove existing empty or full-copy preview databases for this source before enabling masked previews", ErrConflict)
	ErrMaskedPreviewValidationFailed = fmt.Errorf("%w: masked PR preview database validation failed", ErrConflict)
)

// SetMaskedPreviews saves the source owner's policy and enables masked
// previews atomically. An existing preview keeps its pinned copy.
func (s *Service) SetMaskedPreviews(ctx context.Context, accountID, databaseID string, tables []api.ManagedPostgresMaskedTable) (Database, error) {
	store, ok := s.store.(*PostgresStore)
	if !ok {
		return Database{}, ErrMaskedPreviewUnsupported
	}
	policy, err := maskedbranch.Normalize(tables)
	if err != nil {
		return Database{}, err
	}
	source, err := customerDatabase(ctx, s.store, accountID, databaseID)
	if err != nil {
		return Database{}, err
	}
	if !source.Preview.IsZero() {
		return Database{}, ErrPreviewDatabase
	}
	if _, err := s.maskedPreview(source, api.ManagedPostgresPreviewStorageLimitBytes); err != nil {
		return Database{}, err
	}
	return store.setMaskedPreviews(ctx, source, policy, s.now())
}

func (s *Service) maskedPreview(source Database, storageLimit int64) (Database, error) {
	if source.State != StateReady || source.ProviderResourceID == "" {
		return Database{}, ErrUnavailable
	}
	backend, err := s.registry.Resolve(source.BackendID, source.BackendFingerprint)
	if err != nil {
		return Database{}, err
	}
	if _, ok := backend.Provider.(maskedBranchCopier); !ok || backend.Driver != "firecracker" {
		return Database{}, ErrMaskedPreviewUnsupported
	}
	spec := MaskedBranchSpec(source.Spec)
	spec.StorageLimitBytes = min(storageLimit, source.Spec.StorageLimitBytes)
	spec.RestoreWindowSeconds = 0
	if err := backend.Capabilities.SupportsNewDatabase(spec); err != nil {
		return Database{}, err
	}
	return Database{Spec: spec, BackendID: backend.ID, BackendFingerprint: backend.Fingerprint, RestoreSourceDatabaseID: source.ID}, nil
}

func (s *PostgresStore) setMaskedPreviews(ctx context.Context, source Database, policy []api.ManagedPostgresMaskedTable, at time.Time) (Database, error) {
	tx, err := s.catalog.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Database{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	// Preview reservation takes these locks in the same order. No old-mode
	// preview can be reserved between the replacement check and mode update.
	if err := s.lockReservationOwner(ctx, tx, source); err != nil {
		return Database{}, mapPostgresError(err)
	}
	id, err := postgresUUID(source.ID)
	if err != nil {
		return Database{}, err
	}
	account, err := postgresUUID(source.AccountID)
	if err != nil {
		return Database{}, err
	}
	q := new(sqlc.Queries)
	locked, err := q.LockManagedPostgresLifecycleDatabase(ctx, tx, sqlc.LockManagedPostgresLifecycleDatabaseParams{AccountID: account, ID: id})
	if err != nil {
		return Database{}, mapPostgresError(err)
	}
	current := databaseFromSQL(locked)
	if !current.Preview.IsZero() || current.State != StateReady || current.OrgID != source.OrgID || current.ProviderResourceID != source.ProviderResourceID ||
		current.BackendID != source.BackendID || current.BackendFingerprint != source.BackendFingerprint || current.Spec != source.Spec {
		return Database{}, ErrConflict
	}
	var personal, needsReplacement bool
	err = tx.QueryRow(ctx, `
		SELECT o.personal_org, EXISTS (
			SELECT FROM managed_postgres_databases d WHERE d.preview_source_database_id = $1 AND d.deleted_at IS NULL
				AND (d.restore_source_database_id IS DISTINCT FROM $1 OR d.restore_source_resource_id IS NOT NULL OR d.restore_point_in_time IS NOT NULL)
		) FROM orgs o WHERE o.id = $2`, id, locked.OrgID).Scan(&personal, &needsReplacement)
	if err != nil {
		return Database{}, mapPostgresError(err)
	}
	if !personal {
		return Database{}, ErrMaskedPreviewUnsupported
	}
	if needsReplacement {
		return Database{}, ErrPreviewReplacementRequired
	}
	if _, err := tx.Exec(ctx, `INSERT INTO native_postgres_preview_masking_policies AS current (database_id, revision, policy, updated_at)
		VALUES ($1, 1, $2::jsonb, $3)
		ON CONFLICT (database_id) DO UPDATE SET revision = current.revision + 1, policy = EXCLUDED.policy, updated_at = EXCLUDED.updated_at
		WHERE current.policy IS DISTINCT FROM EXCLUDED.policy`, id, policy, at); err != nil {
		return Database{}, mapPostgresError(err)
	}
	row, err := q.SetManagedPostgresDatabasePreviews(ctx, tx, sqlc.SetManagedPostgresDatabasePreviewsParams{
		ID: id, AccountID: account, Previews: string(PreviewsMasked), At: databaseNullableTime(at),
	})
	if err != nil {
		return Database{}, mapPostgresError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Database{}, mapPostgresError(err)
	}
	return databaseFromSQL(row), nil
}
