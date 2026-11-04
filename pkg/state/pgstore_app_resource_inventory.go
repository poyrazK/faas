// adr: 20261103222927
package state

import (
	"context"
	"fmt"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PgStore) ListAppResourceInventory(ctx context.Context, q AppResourceInventoryRequest) (AppResourceInventoryPage, error) {
	if err := q.Validate(); err != nil {
		return AppResourceInventoryPage{}, err
	}
	row, err := sqlc.New().AppResourceInventory(ctx, s.pool, sqlc.AppResourceInventoryParams{
		AccountID: mustPgUUID(q.AccountID), AppID: mustPgUUID(q.AppID), OrgID: q.OrgID, Scope: q.Scope, Kind: q.Kind,
		RowLimit: int32(q.Limit + 1), RowOffset: int32(q.Offset),
	})
	if err != nil {
		return AppResourceInventoryPage{}, fmt.Errorf("state: read app resource inventory: %w", err)
	}
	return DecodeAppResourceInventoryPage(row.Items, row.Total, q)
}
