package managedpostgres

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// RestoreSlots implements RestoreSlotStore (ADR-20261031123259).
func (s *PostgresStore) RestoreSlots(ctx context.Context, limit int) ([]RestoreSlotHolder, error) {
	if limit < 1 || limit > 500 {
		return nil, ErrInvalid
	}
	rows, err := new(sqlc.Queries).ListManagedPostgresRestoreSlots(ctx, s.catalog, int32(limit))
	if err != nil {
		return nil, mapPostgresError(err)
	}
	holders := make([]RestoreSlotHolder, 0, len(rows))
	for _, row := range rows {
		holders = append(holders, RestoreSlotHolder{Database: databaseFromSQL(row.ManagedPostgresDatabase), SubjectID: databaseUUID(row.SubjectID)})
	}
	return holders, nil
}

// CountSubjectDatabases implements RestoreSlotStore with the reservation's
// own count.
func (s *PostgresStore) CountSubjectDatabases(ctx context.Context, subjectID string) (int64, error) {
	subject, err := postgresUUID(subjectID)
	if err != nil {
		return 0, err
	}
	count, err := new(sqlc.Queries).CountManagedPostgresLifecycleDatabases(ctx, s.catalog, subject)
	return count, mapPostgresError(err)
}

// ClearRestoreSlot implements RestoreSlotStore.
func (s *PostgresStore) ClearRestoreSlot(ctx context.Context, databaseID string, now time.Time) (bool, error) {
	id, err := postgresUUID(databaseID)
	if err != nil {
		return false, err
	}
	cleared, err := new(sqlc.Queries).ClearManagedPostgresRestoreSlot(ctx, s.catalog, sqlc.ClearManagedPostgresRestoreSlotParams{ID: id, At: databaseNullableTime(now)})
	return cleared > 0, mapPostgresError(err)
}
