package accountexport

import (
	"context"
	"errors"
	"math"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type ExportLedger interface {
	ListGdprRequestsForAccountPage(context.Context, string, time.Time, string, int) ([]state.GdprRequest, error)
}

// RetryAfter finds the latest export, even when newer receipts are for other
// actions. The count used by the throttle is not a limit on all ledger rows.
func RetryAfter(ctx context.Context, ledger ExportLedger, accountID string, now time.Time) (int, error) {
	const pageSize = 100
	var beforeAt time.Time
	var beforeID string
	for {
		rows, err := ledger.ListGdprRequestsForAccountPage(ctx, accountID, beforeAt, beforeID, pageSize)
		if err != nil {
			return 0, err
		}
		for _, row := range rows {
			if row.Action == state.GdprActionExport {
				seconds := int(math.Ceil(row.RequestedAt.Add(api.ExportRateLimitWindow).Sub(now).Seconds()))
				return max(1, seconds), nil
			}
		}
		if len(rows) < pageSize {
			return 0, state.ErrNotFound
		}
		last := rows[len(rows)-1]
		if last.RequestedAt.Equal(beforeAt) && last.ID == beforeID {
			return 0, errors.New("export ledger cursor did not advance")
		}
		beforeAt, beforeID = last.RequestedAt, last.ID
	}
}
