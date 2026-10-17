package state

// adr: 1548

import (
	"context"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ FunctionMigrationRestartStore = (*PgStore)(nil)

func (s *PgStore) ListRestartFunctionMigrationDestinations(ctx context.Context, node, after string, limit int) ([]FunctionMigrationDestination, error) {
	if !validRestartMigrationUUID(node) || after != "" && !validRestartMigrationUUID(after) || limit <= 0 {
		return nil, ErrInvalidArgument
	}
	rows, err := sqlc.New().ListRestartFunctionMigrationDestinations(ctx, s.pool, sqlc.ListRestartFunctionMigrationDestinationsParams{
		NodeID: mustPgUUID(node), AfterID: mustPgUUID(after), MaxRows: int64(limit),
	})
	if err != nil {
		return nil, mapErr(err)
	}
	result := make([]FunctionMigrationDestination, 0, len(rows))
	for _, row := range rows {
		d, err := decodeFunctionDestination(row)
		if err != nil {
			return nil, err
		}
		result = append(result, d)
	}
	return result, nil
}
