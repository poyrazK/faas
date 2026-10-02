package managedpostgres

import (
	"context"
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
	var afterID pgtype.UUID
	if !after.isZero() {
		var err error
		afterID, err = postgresUUID(after.ID)
		if err != nil {
			return nil, err
		}
	}
	rows, err := new(sqlc.Queries).ListManagedPostgresLifecycleUsageDatabases(ctx, s.pool, sqlc.ListManagedPostgresLifecycleUsageDatabasesParams{
		FirstPage: after.isZero(), AfterTime: databaseNullableTime(after.UpdatedAt), AfterID: afterID, RowLimit: int32(limit)})
	return databasesFromSQL(rows), mapPostgresError(err)
}

func (s *PostgresStore) RecordUsage(ctx context.Context, records []UsageRecord) error {
	if len(records) == 0 {
		return ErrInvalid
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return mapPostgresError(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	for _, record := range records {
		if err := record.Validate(); err != nil {
			return err
		}
		accountID, err := postgresUUID(record.AccountID)
		if err != nil {
			return err
		}
		databaseID, err := postgresUUID(record.DatabaseID)
		if err != nil {
			return err
		}
		var account, backend, fingerprint string
		if err := tx.QueryRow(ctx,
			`SELECT account_id::text, backend_id, backend_fingerprint
			 FROM managed_postgres_databases WHERE id = $1 AND state <> 'deleted' FOR SHARE`,
			databaseID,
		).Scan(&account, &backend, &fingerprint); err != nil {
			return mapPostgresError(err)
		}
		if account != record.AccountID || backend != record.BackendID || fingerprint != record.BackendFingerprint {
			return ErrConflict
		}
		_, err = tx.Exec(ctx,
			`INSERT INTO managed_postgres_usage (
				account_id, database_id, backend_id, backend_fingerprint,
				window_from, window_to, observed_at, meter, quantity, cost_millicents
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
			ON CONFLICT (database_id, window_from, window_to, meter) DO UPDATE SET
				observed_at = EXCLUDED.observed_at,
				quantity = EXCLUDED.quantity,
				cost_millicents = EXCLUDED.cost_millicents`,
			accountID, databaseID, record.BackendID, record.BackendFingerprint,
			record.WindowFrom, record.WindowTo, record.ObservedAt, string(record.Meter),
			record.Quantity, record.CostMillicents,
		)
		if err != nil {
			return mapPostgresError(err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return mapPostgresError(err)
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
	var snapshot UsageSnapshot
	var lastObserved pgtype.Timestamptz
	if err := s.pool.QueryRow(ctx,
		`SELECT
			count(DISTINCT d.id) FILTER (WHERE d.state = 'ready')::int,
			COALESCE(sum(u.quantity) FILTER (WHERE u.meter = 'compute_unit_seconds'), 0)::bigint,
			COALESCE(sum(u.quantity) FILTER (WHERE u.meter = 'storage_byte_seconds'), 0)::bigint,
			COALESCE(sum(u.quantity) FILTER (WHERE u.meter = 'history_byte_seconds'), 0)::bigint,
			COALESCE(sum(u.quantity) FILTER (WHERE u.meter = 'egress_bytes'), 0)::bigint,
			COALESCE(sum(u.cost_millicents), 0)::bigint,
			max(u.observed_at)
		 FROM managed_postgres_databases d
		 LEFT JOIN managed_postgres_usage u
			 ON u.database_id = d.id AND u.window_from >= $2 AND u.window_from < $3
		 WHERE d.account_id = $1 AND d.state <> 'deleted'`,
		account, periodStart, periodEnd,
	).Scan(&snapshot.ReadyDatabases, &snapshot.ComputeUnitSeconds, &snapshot.StorageByteSeconds, &snapshot.HistoryByteSeconds, &snapshot.EgressBytes, &snapshot.CostMillicents, &lastObserved); err != nil {
		return UsageSnapshot{}, mapPostgresError(err)
	}
	snapshot.PeriodStart = periodStart
	if lastObserved.Valid {
		snapshot.LastObservedAt = lastObserved.Time
	}
	return snapshot, nil
}
