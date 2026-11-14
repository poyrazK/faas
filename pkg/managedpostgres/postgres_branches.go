package managedpostgres

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ BranchExpiryStore = (*PostgresStore)(nil)

// ExpiredBranchesPage implements BranchExpiryStore (ADR-20261106103654).
func (s *PostgresStore) ExpiredBranchesPage(ctx context.Context, now time.Time, after BranchExpiryCursor, limit int) ([]Database, error) {
	if limit < 1 || limit > 500 || now.IsZero() || after.ExpiresAt.IsZero() != (after.ID == "") {
		return nil, ErrInvalid
	}
	afterID := databaseNullableUUID(after.ID)
	if after.ID != "" && !afterID.Valid {
		return nil, ErrInvalid
	}
	rows, err := new(sqlc.Queries).ListManagedPostgresExpiredDatabasePage(ctx, s.catalog, sqlc.ListManagedPostgresExpiredDatabasePageParams{
		Now: databaseNullableTime(now), AfterExpiresAt: databaseNullableTime(after.ExpiresAt), AfterID: afterID, BatchSize: int32(limit),
	})
	if err != nil {
		return nil, mapPostgresError(err)
	}
	return databasesFromSQL(rows), nil
}
