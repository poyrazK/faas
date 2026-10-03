package managedpostgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

var _ UsageStore = (*PostgresStore)(nil)

func (s *PostgresStore) ListUsageDatabases(ctx context.Context, after UsageDatabaseCursor, limit int) ([]Database, error) {
	if limit < 1 || limit > 100 {
		return nil, ErrInvalid
	}
	var (
		rows pgx.Rows
		err  error
	)
	if after.isZero() {
		rows, err = s.pool.Query(ctx,
			`SELECT `+postgresDatabaseColumns+` FROM managed_postgres_databases
			 WHERE state = 'ready' AND provider_resource_id IS NOT NULL
			 ORDER BY updated_at, id LIMIT $1`, limit,
		)
	} else {
		afterID, idErr := postgresUUID(after.ID)
		if idErr != nil {
			return nil, idErr
		}
		rows, err = s.pool.Query(ctx,
			`SELECT `+postgresDatabaseColumns+` FROM managed_postgres_databases
			 WHERE state = 'ready' AND provider_resource_id IS NOT NULL
			   AND (updated_at, id) > ($1, $2)
			 ORDER BY updated_at, id LIMIT $3`, after.UpdatedAt, afterID, limit,
		)
	}
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

func (s *PostgresStore) UsageProgress(ctx context.Context, accountID, databaseID string, window time.Duration) (UsageProgress, error) {
	account, err := postgresUUID(accountID)
	if err != nil {
		return UsageProgress{}, err
	}
	database, err := postgresUUID(databaseID)
	if err != nil {
		return UsageProgress{}, err
	}
	var from, until, observed pgtype.Timestamptz
	var source pgtype.Text
	err = s.pool.QueryRow(ctx, `SELECT c.collected_from, c.collected_until, c.observed_at, c.source_database_id::text
 FROM managed_postgres_databases d LEFT JOIN managed_postgres_usage_coverage c
 ON c.database_id = d.id AND c.window_seconds = $3 WHERE d.account_id = $1 AND d.id = $2`,
		account, database, int64(window/time.Second)).Scan(&from, &until, &observed, &source)
	if err != nil {
		return UsageProgress{}, mapPostgresError(err)
	}
	return usageProgressFromColumns(window, from, until, observed, source), nil
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
	var owner, backend, fingerprint string
	var createdAt time.Time
	if err := tx.QueryRow(ctx, `SELECT account_id::text, backend_id, backend_fingerprint, created_at
 FROM managed_postgres_databases WHERE id = $1 AND state <> 'deleted' FOR UPDATE`, database).
		Scan(&owner, &backend, &fingerprint, &createdAt); err != nil {
		return mapPostgresError(err)
	}
	if owner != first.AccountID || backend != first.BackendID || fingerprint != first.BackendFingerprint {
		return ErrConflict
	}
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
	if window < time.Hour || window > 24*time.Hour || window%time.Second != 0 || databaseID == sourceID {
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
	tag, err := s.pool.Exec(ctx, `WITH RECURSIVE ancestry AS (
 SELECT restore_source_database_id AS id FROM managed_postgres_databases WHERE id = $2 AND account_id = $1
 UNION
 SELECT d.restore_source_database_id FROM managed_postgres_databases d JOIN ancestry a ON d.id = a.id
 WHERE d.account_id = $1
 )
 INSERT INTO managed_postgres_usage_coverage (database_id, window_seconds, source_database_id)
 SELECT d.id, $4, s.id FROM managed_postgres_databases d JOIN managed_postgres_databases s
 ON s.id = $3 AND s.account_id = d.account_id AND s.backend_id = d.backend_id AND s.backend_fingerprint = d.backend_fingerprint
 WHERE d.id = $2 AND d.account_id = $1 AND d.state = 'ready' AND s.state = 'ready'
 AND s.id IN (SELECT id FROM ancestry)
 ON CONFLICT (database_id, window_seconds) DO UPDATE SET
 source_database_id = EXCLUDED.source_database_id, collected_from = NULL, collected_until = NULL,
 observed_at = NULL, updated_at = now()`, account, database, source, int64(window/time.Second))
	if err != nil {
		return mapPostgresError(err)
	}
	if tag.RowsAffected() != 1 {
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
	rows, err := tx.Query(ctx, `SELECT c.window_seconds,
 COALESCE(s.collected_from, c.collected_from), COALESCE(s.collected_until, c.collected_until),
 COALESCE(s.observed_at, c.observed_at), c.source_database_id::text
 FROM managed_postgres_databases d
 LEFT JOIN LATERAL (SELECT * FROM managed_postgres_usage_coverage WHERE database_id = d.id
 ORDER BY updated_at DESC, window_seconds DESC LIMIT 1) c ON true
 LEFT JOIN managed_postgres_usage_coverage s ON s.database_id = c.source_database_id AND s.window_seconds = c.window_seconds
 WHERE d.account_id = $1 AND d.state = 'ready' ORDER BY d.id`, account)
	if err != nil {
		return UsageSnapshot{}, mapPostgresError(err)
	}
	for rows.Next() {
		var seconds pgtype.Int8
		var from, until, observed pgtype.Timestamptz
		var source pgtype.Text
		if err := rows.Scan(&seconds, &from, &until, &observed, &source); err != nil {
			rows.Close()
			return UsageSnapshot{}, mapPostgresError(err)
		}
		progress := usageProgressFromColumns(time.Duration(seconds.Int64)*time.Second, from, until, observed, source)
		if snapshot.ReadyDatabases == 0 || progress.ObservedAt.Before(snapshot.LastObservedAt) {
			snapshot.LastObservedAt = progress.ObservedAt
		}
		snapshot.Databases = append(snapshot.Databases, progress)
		snapshot.ReadyDatabases++
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return UsageSnapshot{}, mapPostgresError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return UsageSnapshot{}, mapPostgresError(err)
	}
	return snapshot, nil
}
