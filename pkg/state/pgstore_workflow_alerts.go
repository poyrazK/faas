package state

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PgStore) WorkflowAlertSnapshot(ctx context.Context, accountID, appID string, since, now time.Time) (WorkflowAlertSnapshot, error) {
	ctx, cancel := context.WithTimeout(ctx, api.WorkflowAlertSnapshotReadTimeout)
	defer cancel()
	row, err := sqlc.New().WorkflowAlertSnapshot(ctx, s.pool, sqlc.WorkflowAlertSnapshotParams{
		AccountID: mustPgUUID(accountID), AppID: mustPgUUID(appID), SinceAt: pgtype.Timestamptz{Time: since, Valid: true}, NowAt: pgtype.Timestamptz{Time: now, Valid: true},
		StaleMs: int64(WorkflowRunStaleAfter / time.Millisecond),
	})
	if err != nil {
		return WorkflowAlertSnapshot{}, fmt.Errorf("state: read workflow alert signals: %w", err)
	}
	return WorkflowAlertSnapshot{Failures: row.Failures, QuotaSkips: row.QuotaSkips, PendingAgeSeconds: row.PendingAgeSeconds, WaitingAgeSeconds: row.WaitingAgeSeconds, DueAgeSeconds: row.DueAgeSeconds}, nil
}
