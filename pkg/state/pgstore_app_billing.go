package state

import (
	"context"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ AppBillingWindowStore = (*PgStore)(nil)

func (s *PgStore) ListDeletedAppsInBillingWindow(ctx context.Context, start, end time.Time) ([]App, error) {
	if start.IsZero() || !start.Before(end) || end.Sub(start) > time.Minute {
		return nil, ErrInvalidArgument
	}
	ids, err := sqlc.New().DeletedAppIDsInBillingWindow(ctx, s.pool, sqlc.DeletedAppIDsInBillingWindowParams{WindowStart: financialTimestamp(start), WindowEnd: financialTimestamp(end)})
	if err != nil {
		return nil, fmt.Errorf("state: app billing window: %w", err)
	}
	apps := make([]App, 0, len(ids))
	for _, id := range ids {
		app, err := s.AppByID(ctx, pgUUIDString(id))
		if err != nil {
			return nil, fmt.Errorf("state: retained billing app: %w", err)
		}
		apps = append(apps, app)
	}
	return apps, nil
}
