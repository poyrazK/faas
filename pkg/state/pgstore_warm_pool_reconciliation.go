package state

import (
	"context"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PgStore) WarmPoolReconciliationAppIDs(ctx context.Context, nodeID string) ([]string, error) {
	ids, err := sqlc.New().ListWarmPoolReconciliationAppIDs(ctx, s.pool, nodeID)
	return ids, mapErr(err)
}
