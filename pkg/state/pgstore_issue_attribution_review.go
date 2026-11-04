package state

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// reviewHistoricalIssueAttribution revisits evidence accepted before the
// request/trace consistency checks. Missing evidence is not itself a conflict:
// enrichOneIssueAttribution preserves established identity in that case.
func (s *PgStore) reviewHistoricalIssueAttribution(ctx context.Context, now time.Time) error {
	deadline := time.Now().Add(api.IssueAttributionReviewBudget)
	q := sqlc.New()
	blocked := make(map[pgtype.UUID]bool)
	var excluded []pgtype.UUID
	for page := 0; page < api.IssueAttributionReviewMaxBatches && time.Now().Before(deadline); page++ {
		queryCtx, cancel := context.WithDeadline(ctx, deadline)
		rows, err := q.IssueHistoricalAttributionEvents(queryCtx, s.pool, sqlc.IssueHistoricalAttributionEventsParams{BatchLimit: api.IssueAttributionReviewBatch, ExcludedAppIds: excluded})
		cancel()
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
				return nil
			}
			return err
		}
		for _, historical := range rows {
			if time.Now().After(deadline) {
				return nil
			}
			if blocked[historical.AppID] {
				continue
			}
			rowCtx, rowCancel := context.WithDeadline(ctx, minTime(deadline, time.Now().Add(api.IssueAttributionRowTimeout)))
			err := s.enrichOneIssueAttribution(rowCtx, q, sqlc.IssueUnattributedEventsRow(historical), now)
			rowCancel()
			if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
				blocked[historical.AppID] = true
				excluded = append(excluded, historical.AppID)
				continue
			}
			if err != nil {
				return err
			}
		}
		if len(rows) < api.IssueAttributionReviewBatch {
			break
		}
	}
	return nil
}
