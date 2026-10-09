package state

import (
	"context"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// ProfileRequestCountReader supplies weighted request counts for the exact
// deployment and wall-clock window used by a CPU profile comparison.
type ProfileRequestCountReader interface {
	ProfileRequestCount(context.Context, string, string, api.ProfileQuery) (int64, bool, error)
}

func (s *PgStore) ProfileRequestCount(ctx context.Context, accountID, appID string, window api.ProfileQuery) (int64, bool, error) {
	row, err := sqlc.New().ProfileRequestCount(ctx, s.pool, sqlc.ProfileRequestCountParams{
		Route: window.Route, AccountID: accountID, AppID: appID, DeploymentID: window.DeploymentID,
		StartAt: profileCheckTime(window.Start), EndAt: profileCheckTime(window.End),
	})
	if err != nil {
		return 0, false, fmt.Errorf("read deployment request count for CPU profiling: %w", err)
	}
	return row.Requests, row.Observations > 0, nil
}
