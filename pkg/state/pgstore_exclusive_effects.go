// adr: 488
package state

import (
	"context"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PgStore) OperationEffectDeliveryAllowed(ctx context.Context, id string) (bool, error) {
	row, err := sqlc.New().OperationEffectDeliveryAllowed(ctx, s.pool, id)
	if err != nil {
		return false, err
	}
	if !row.Managed.Valid || !row.Managed.Bool {
		return false, ErrNotOperationEffect
	}
	return row.Allowed.Valid && row.Allowed.Bool, nil
}
