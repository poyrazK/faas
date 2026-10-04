package managedpostgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ UsageStore = (*PostgresStore)(nil)

func (s *PostgresStore) ListUsageDatabases(ctx context.Context, after UsageDatabaseCursor, limit int) ([]Database, error) {
	if limit < 1 || limit > 100 {
		return nil, ErrInvalid
	}
	params := sqlc.ListManagedPostgresUsageResourcesParams{PageLimit: int32(limit)}
	if !after.isZero() {
		id, err := postgresUUID(after.ID)
		if err != nil {
			return nil, err
		}
		params.AfterID = id
		params.AfterUpdatedAt = pgtype.Timestamptz{Time: after.UpdatedAt, Valid: true}
	}
	rows, err := sqlc.New().ListManagedPostgresUsageResources(ctx, s.pool, params)
	if err != nil {
		return nil, mapPostgresError(err)
	}
	items := make([]Database, 0, len(rows))
	for _, row := range rows {
		items = append(items, databaseFromRow(row))
	}
	return items, nil
}

func databaseFromRow(row sqlc.ManagedPostgresDatabase) Database {
	database := Database{
		ID: cutoverUUID(row.ID), AccountID: cutoverUUID(row.AccountID), Name: row.Name,
		Spec: Spec{Region: row.Region, PostgresMajor: int(row.PostgresMajor), Class: ServiceClass(row.ServiceClass),
			Availability: Availability(row.Availability), ScaleToZero: row.ScaleToZero,
			StorageLimitBytes: row.StorageLimitBytes, RestoreWindowSeconds: row.RestoreWindowSeconds},
		BackendID: row.BackendID, BackendFingerprint: row.BackendFingerprint,
		ProviderResourceID: row.ProviderResourceID.String, RestoreSourceResourceID: row.RestoreSourceResourceID.String,
		AccountingRequired: row.AccountingRequired,
		RestorePointInTime: row.RestorePointInTime.Time, State: State(row.State),
		DesiredGeneration: row.DesiredGeneration, ObservedGeneration: row.ObservedGeneration,
		LastErrorCode: row.LastErrorCode.String, LeaseToken: row.LeaseToken.String, LeaseUntil: row.LeaseUntil.Time,
		AttemptCount: row.AttemptCount, RetryAt: row.RetryAt.Time, CreatedAt: row.CreatedAt.Time, UpdatedAt: row.UpdatedAt.Time,
	}
	if row.RestoreSourceDatabaseID.Valid {
		database.RestoreSourceDatabaseID = cutoverUUID(row.RestoreSourceDatabaseID)
	}
	if row.DeletedAt.Valid {
		database.DeletedAt = &row.DeletedAt.Time
	}
	return database
}

func (s *PostgresStore) UsageProgress(ctx context.Context, accountID, databaseID string, window time.Duration) (UsageProgress, error) {
	account, err := postgresUUID(accountID)
	if err != nil {
		return UsageProgress{}, err
	}
	database, err := postgresUUID(databaseID)
	if err != nil {
		return UsageProgress{}, err
	}
	row, err := sqlc.New().GetManagedPostgresUsageProgress(ctx, s.pool, sqlc.GetManagedPostgresUsageProgressParams{
		AccountID: account, DatabaseID: database, WindowSeconds: int64(window / time.Second),
	})
	if err != nil {
		return UsageProgress{}, mapPostgresError(err)
	}
	progress := usageProgressFromColumns(window, row.CollectedFrom, row.CollectedUntil, row.ObservedAt,
		pgtype.Text{String: row.SourceDatabaseID, Valid: row.SourceDatabaseID != ""})
	progress.CorrectionObservedAt = row.CorrectionObservedAt.Time
	return progress, nil
}

func usageProgressFromColumns(window time.Duration, from, until, observed pgtype.Timestamptz, source pgtype.Text) UsageProgress {
	progress := UsageProgress{Window: window}
	if from.Valid {
		progress.CollectedFrom = from.Time
	}
	if until.Valid {
		progress.CollectedUntil = until.Time
	}
	if observed.Valid {
		progress.ObservedAt = observed.Time
	}
	if source.Valid {
		progress.SourceDatabaseID = source.String
	}
	return progress
}

func (s *PostgresStore) RecordUsage(ctx context.Context, records []UsageRecord) error {
	key, err := validateUsageRecords(records)
	if err != nil {
		return err
	}
	first := records[0]
	account, err := postgresUUID(first.AccountID)
	if err != nil {
		return err
	}
	database, err := postgresUUID(first.DatabaseID)
	if err != nil {
		return err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return mapPostgresError(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	// Serialize ledger replacement and coverage advancement across collectors.
	row, err := sqlc.New().LockManagedPostgresUsageResource(ctx, tx, database)
	if err != nil {
		return mapPostgresError(err)
	}
	resource := databaseFromRow(row)
	if resource.AccountID != first.AccountID || resource.BackendID != first.BackendID || resource.BackendFingerprint != first.BackendFingerprint {
		return ErrConflict
	}
	if err := validateUsageDatabaseWindow(resource, first); err != nil {
		return err
	}
	createdAt := resource.CreatedAt
	var from, until, observed pgtype.Timestamptz
	var source pgtype.Text
	err = tx.QueryRow(ctx, `SELECT collected_from, collected_until, observed_at, source_database_id::text
 FROM managed_postgres_usage_coverage WHERE database_id = $1 AND window_seconds = $2`, database, int64(key.window/time.Second)).
		Scan(&from, &until, &observed, &source)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return mapPostgresError(err)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		var incompatibleWindow bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM managed_postgres_usage
 WHERE database_id = $1 AND window_to - window_from <> $2 * interval '1 second')`,
			database, int64(key.window/time.Second)).Scan(&incompatibleWindow); err != nil {
			return mapPostgresError(err)
		}
		if incompatibleWindow {
			return ErrConflict
		}
	}
	progress, err := advanceUsageProgress(usageProgressFromColumns(key.window, from, until, observed, source), first, createdAt)
	if err != nil {
		return err
	}
	for _, record := range records {
		_, err = tx.Exec(ctx, `INSERT INTO managed_postgres_usage (
 account_id, database_id, backend_id, backend_fingerprint, window_from, window_to, observed_at, meter, quantity, cost_millicents
 ) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
 ON CONFLICT (database_id, window_from, window_to, meter) DO UPDATE SET
 observed_at = EXCLUDED.observed_at, quantity = EXCLUDED.quantity, cost_millicents = EXCLUDED.cost_millicents
 WHERE managed_postgres_usage.observed_at <= EXCLUDED.observed_at`,
			account, database, record.BackendID, record.BackendFingerprint, record.WindowFrom, record.WindowTo,
			record.ObservedAt, string(record.Meter), record.Quantity, record.CostMillicents)
		if err != nil {
			return mapPostgresError(err)
		}
	}
	_, err = tx.Exec(ctx, `INSERT INTO managed_postgres_usage_coverage
 (database_id, window_seconds, collected_from, collected_until, observed_at)
 VALUES ($1,$2,$3,$4,$5)
 ON CONFLICT (database_id, window_seconds) DO UPDATE SET
 collected_from = EXCLUDED.collected_from, collected_until = EXCLUDED.collected_until,
 observed_at = EXCLUDED.observed_at, updated_at = now()`,
		database, int64(key.window/time.Second), progress.CollectedFrom, progress.CollectedUntil, progress.ObservedAt)
	if err != nil {
		return mapPostgresError(err)
	}
	return mapPostgresError(tx.Commit(ctx))
}

func (s *PostgresStore) RecordSharedUsage(ctx context.Context, accountID, databaseID, sourceID string, window time.Duration) error {
	if !validUsageWindow(window) || databaseID == sourceID {
		return ErrInvalid
	}
	account, err := postgresUUID(accountID)
	if err != nil {
		return err
	}
	database, err := postgresUUID(databaseID)
	if err != nil {
		return err
	}
	source, err := postgresUUID(sourceID)
	if err != nil {
		return err
	}
	count, err := sqlc.New().RecordManagedPostgresSharedUsage(ctx, s.pool, sqlc.RecordManagedPostgresSharedUsageParams{
		AccountID: account, DatabaseID: database, SourceID: source, WindowSeconds: int64(window / time.Second),
	})
	if err != nil {
		return mapPostgresError(err)
	}
	if count != 1 {
		return ErrConflict
	}
	return nil
}

func (s *PostgresStore) UsageSnapshot(ctx context.Context, accountID string, periodStart time.Time) (UsageSnapshot, error) {
	account, err := postgresUUID(accountID)
	if err != nil || periodStart.IsZero() {
		return UsageSnapshot{}, ErrInvalid
	}
	periodStart = monthStart(periodStart)
	periodEnd := monthStart(periodStart.AddDate(0, 1, 0))
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return UsageSnapshot{}, mapPostgresError(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	snapshot := UsageSnapshot{PeriodStart: periodStart}
	// Resource deletion must not erase consumption already incurred this month.
	err = tx.QueryRow(ctx, `SELECT
 COALESCE(sum(quantity) FILTER (WHERE meter = 'compute_unit_seconds'), 0)::bigint,
 COALESCE(sum(quantity) FILTER (WHERE meter = 'storage_byte_seconds'), 0)::bigint,
 COALESCE(sum(quantity) FILTER (WHERE meter = 'history_byte_seconds'), 0)::bigint,
 COALESCE(sum(quantity) FILTER (WHERE meter = 'egress_bytes'), 0)::bigint,
 COALESCE(sum(cost_millicents), 0)::bigint
 FROM managed_postgres_usage WHERE account_id = $1 AND window_from >= $2 AND window_from < $3`,
		account, periodStart, periodEnd).Scan(&snapshot.ComputeUnitSeconds, &snapshot.StorageByteSeconds,
		&snapshot.HistoryByteSeconds, &snapshot.EgressBytes, &snapshot.CostMillicents)
	if err != nil {
		return UsageSnapshot{}, mapPostgresError(err)
	}
	rows, err := sqlc.New().ListManagedPostgresAccountingCoverage(ctx, tx, account)
	if err != nil {
		return UsageSnapshot{}, mapPostgresError(err)
	}
	for _, row := range rows {
		progress := usageProgressFromColumns(time.Duration(row.WindowSeconds)*time.Second,
			row.CollectedFrom, row.CollectedUntil, row.ObservedAt,
			pgtype.Text{String: row.SourceDatabaseID, Valid: row.SourceDatabaseID != ""})
		progress.CorrectionObservedAt = row.CorrectionObservedAt.Time
		progress.Terminal = row.AccountingState == string(StateDeleted)
		progress.Unresolved = row.Unresolved
		progress.EndedAt = row.EndedAt.Time
		if len(snapshot.Databases) == 0 || progress.ObservedAt.Before(snapshot.LastObservedAt) {
			snapshot.LastObservedAt = progress.ObservedAt
		}
		snapshot.Databases = append(snapshot.Databases, progress)
		if row.State == string(StateReady) {
			snapshot.ReadyDatabases++
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return UsageSnapshot{}, mapPostgresError(err)
	}
	return snapshot, nil
}
