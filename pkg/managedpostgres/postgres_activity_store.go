package managedpostgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ ComputeActivityStore = (*PostgresStore)(nil)

func (s *PostgresStore) RecordComputeEvents(ctx context.Context, databaseID string, events []ComputeEvent) (int, error) {
	id, err := postgresUUID(databaseID)
	if err != nil {
		return 0, err
	}
	recorded := 0
	for _, event := range sortComputeEvents(events) {
		switch event.Kind {
		case ComputeEventStart:
			ok, err := sqlc.New().RecordManagedPostgresComputeStart(ctx, s.catalog, sqlc.RecordManagedPostgresComputeStartParams{
				DatabaseID: id, ProviderOperationID: event.OperationID, StartedAt: databaseNullableTime(event.At)})
			if errors.Is(err, pgx.ErrNoRows) {
				continue // already recorded, or the database is gone
			}
			if err != nil {
				return recorded, mapPostgresError(err)
			}
			if ok {
				recorded++
			}
		case ComputeEventSuspend:
			if _, err := sqlc.New().RecordManagedPostgresComputeSuspend(ctx, s.catalog, sqlc.RecordManagedPostgresComputeSuspendParams{
				DatabaseID: id, SuspendedAt: databaseNullableTime(event.At)}); err != nil {
				return recorded, mapPostgresError(err)
			}
		}
	}
	return recorded, nil
}

func (s *PostgresStore) PruneComputeStarts(ctx context.Context, before time.Time, limit int) (int64, error) {
	n, err := sqlc.New().PruneManagedPostgresComputeStarts(ctx, s.catalog, sqlc.PruneManagedPostgresComputeStartsParams{
		Cutoff: databaseNullableTime(before), BatchSize: int32(limit)})
	return n, mapPostgresError(err)
}

func (s *PostgresStore) ComputeStarts(ctx context.Context, databaseID string, since time.Time, limit int) ([]ComputeStart, error) {
	id, err := postgresUUID(databaseID)
	if err != nil {
		return nil, err
	}
	rows, err := sqlc.New().ListManagedPostgresComputeStarts(ctx, s.catalog, sqlc.ListManagedPostgresComputeStartsParams{
		DatabaseID: id, Since: databaseNullableTime(since), RowLimit: int32(limit)})
	if err != nil {
		return nil, mapPostgresError(err)
	}
	out := make([]ComputeStart, 0, len(rows))
	for _, row := range rows {
		out = append(out, ComputeStart{OperationID: row.ProviderOperationID, StartedAt: row.StartedAt.Time,
			SuspendedAt: row.SuspendedAt.Time, Cause: row.Cause, AppID: postgresUUIDString(row.CauseAppID),
			AppSlug: row.CauseAppSlug, Detail: row.CauseDetail})
	}
	return out, nil
}

func (s *PostgresStore) ComputeStartCauses(ctx context.Context, databaseID string, since, now time.Time) ([]ComputeStartCause, error) {
	id, err := postgresUUID(databaseID)
	if err != nil {
		return nil, err
	}
	rows, err := sqlc.New().ManagedPostgresComputeStartCauses(ctx, s.catalog, sqlc.ManagedPostgresComputeStartCausesParams{
		DatabaseID: id, Since: databaseNullableTime(since), Now: databaseNullableTime(now)})
	if err != nil {
		return nil, mapPostgresError(err)
	}
	out := make([]ComputeStartCause, 0, len(rows))
	for _, row := range rows {
		out = append(out, ComputeStartCause{Cause: row.Cause, AppID: postgresUUIDString(row.CauseAppID), AppSlug: row.CauseAppSlug,
			Detail: row.CauseDetail, Starts: int(row.Starts), AwakeSeconds: row.AwakeSeconds, FirstStartedAt: row.FirstStartedAt.Time})
	}
	return out, nil
}

func (s *PostgresStore) AccountActivityTotals(ctx context.Context, accountID string, since, now time.Time) (map[string]DatabaseActivityTotals, error) {
	id, err := postgresUUID(accountID)
	if err != nil {
		return nil, err
	}
	out := map[string]DatabaseActivityTotals{}
	compute, err := sqlc.New().ManagedPostgresDatabaseComputeUnitSeconds(ctx, s.catalog, sqlc.ManagedPostgresDatabaseComputeUnitSecondsParams{
		AccountID: id, Since: databaseNullableTime(since)})
	if err != nil {
		return nil, mapPostgresError(err)
	}
	for _, row := range compute {
		databaseID := postgresUUIDString(row.DatabaseID)
		out[databaseID] = DatabaseActivityTotals{DatabaseID: databaseID, ComputeUnitSeconds: row.ComputeUnitSeconds}
	}
	starts, err := sqlc.New().ManagedPostgresAccountComputeStarts(ctx, s.catalog, sqlc.ManagedPostgresAccountComputeStartsParams{
		AccountID: id, Since: databaseNullableTime(since), Now: databaseNullableTime(now)})
	if err != nil {
		return nil, mapPostgresError(err)
	}
	for _, row := range starts {
		databaseID := postgresUUIDString(row.DatabaseID)
		totals := out[databaseID]
		totals.DatabaseID, totals.Starts, totals.AwakeSeconds = databaseID, int(row.Starts), row.AwakeSeconds
		out[databaseID] = totals
	}
	return out, nil
}
