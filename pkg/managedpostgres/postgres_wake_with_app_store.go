package managedpostgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ WakeWithAppStore = (*PostgresStore)(nil)

func (s *PostgresStore) SetWakeWithApp(ctx context.Context, databaseID string, enabled bool) error {
	id, err := postgresUUID(databaseID)
	if err != nil {
		return err
	}
	if !enabled {
		return mapPostgresError(sqlc.New().DisableManagedPostgresWakeWithApp(ctx, s.catalog, id))
	}
	return mapPostgresError(sqlc.New().EnableManagedPostgresWakeWithApp(ctx, s.catalog, id))
}

func (s *PostgresStore) WakeWithApp(ctx context.Context, databaseIDs []string) (map[string]bool, error) {
	ids := make([]pgtype.UUID, 0, len(databaseIDs))
	for _, databaseID := range databaseIDs {
		id, err := postgresUUID(databaseID)
		if err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	out := make(map[string]bool, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := sqlc.New().ManagedPostgresWakeWithApp(ctx, s.catalog, ids)
	if err != nil {
		return nil, mapPostgresError(err)
	}
	for _, row := range rows {
		out[postgresUUIDString(row)] = true
	}
	return out, nil
}

func (s *PostgresStore) WakeWithAppApps(ctx context.Context) (map[string]bool, error) {
	rows, err := sqlc.New().ManagedPostgresWakeWithAppApps(ctx, s.catalog)
	if err != nil {
		return nil, mapPostgresError(err)
	}
	out := make(map[string]bool, len(rows))
	for _, row := range rows {
		out[postgresUUIDString(row)] = true
	}
	return out, nil
}

func (s *PostgresStore) ClaimWakeStarts(ctx context.Context, appID string, gap time.Duration) ([]WakeStartClaim, error) {
	id, err := postgresUUID(appID)
	if err != nil {
		return nil, err
	}
	rows, err := sqlc.New().ClaimManagedPostgresWakeStarts(ctx, s.catalog,
		sqlc.ClaimManagedPostgresWakeStartsParams{AppID: id, GapSeconds: gap.Seconds()})
	if err != nil {
		return nil, mapPostgresError(err)
	}
	out := make([]WakeStartClaim, 0, len(rows))
	for _, row := range rows {
		out = append(out, WakeStartClaim{DatabaseID: postgresUUIDString(row.ID), BackendID: row.BackendID,
			BackendFingerprint: row.BackendFingerprint, ProviderResourceID: row.ProviderResourceID})
	}
	return out, nil
}
