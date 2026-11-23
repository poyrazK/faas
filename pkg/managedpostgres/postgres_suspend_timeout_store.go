package managedpostgres

import (
	"context"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ SuspendTimeoutStore = (*PostgresStore)(nil)

func (s *PostgresStore) SetSuspendTimeout(ctx context.Context, databaseID string, seconds *int64) error {
	id, err := postgresUUID(databaseID)
	if err != nil {
		return err
	}
	if seconds == nil {
		return mapPostgresError(sqlc.New().ClearManagedPostgresSuspendTimeout(ctx, s.catalog, id))
	}
	if !ValidCustomerSuspendTimeout(*seconds) {
		return ErrInvalid
	}
	return mapPostgresError(sqlc.New().SetManagedPostgresSuspendTimeout(ctx, s.catalog,
		sqlc.SetManagedPostgresSuspendTimeoutParams{DatabaseID: id, Seconds: int32(*seconds)}))
}

func (s *PostgresStore) SuspendTimeouts(ctx context.Context, databaseIDs []string) (map[string]int64, error) {
	ids := make([]pgtype.UUID, 0, len(databaseIDs))
	for _, databaseID := range databaseIDs {
		id, err := postgresUUID(databaseID)
		if err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	out := make(map[string]int64, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := sqlc.New().ManagedPostgresSuspendTimeouts(ctx, s.catalog, ids)
	if err != nil {
		return nil, mapPostgresError(err)
	}
	for _, row := range rows {
		out[postgresUUIDString(row.DatabaseID)] = int64(row.Seconds)
	}
	return out, nil
}
