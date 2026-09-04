package billing

import (
	"context"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// OverageMBSecondsForRange returns the incremental overage in a UTC
// half-open range. Gregale's included allowance resets at the start of each
// UTC calendar month, so a range crossing month-end is evaluated month by
// month rather than by subtracting one allowance from the whole range.
//
// UsageByHour is intentionally used instead of UsageByAccount: invoices and
// provider reconciliation need the exact [start, end) boundary, while the
// latter is a monthly aggregate and cannot exclude a future portion of a
// month.
func OverageMBSecondsForRange(ctx context.Context, store state.Store, acct state.Account, start, end time.Time) (int64, error) {
	if store == nil {
		return 0, fmt.Errorf("billing: usage range store is nil")
	}
	start = start.UTC()
	end = end.UTC()
	if !start.Before(end) {
		return 0, nil
	}

	var total int64
	for monthStart := utcMonthStart(start); monthStart.Before(end); monthStart = monthStart.AddDate(0, 1, 0) {
		monthEnd := monthStart.AddDate(0, 1, 0)
		segmentStart := start
		if segmentStart.Before(monthStart) {
			segmentStart = monthStart
		}
		segmentEnd := end
		if segmentEnd.After(monthEnd) {
			segmentEnd = monthEnd
		}

		prior, err := sumUsage(ctx, store, acct.ID, monthStart, segmentStart)
		if err != nil {
			return 0, fmt.Errorf("billing: usage before %s: %w", monthStart.Format(time.RFC3339), err)
		}
		current, err := sumUsage(ctx, store, acct.ID, segmentStart, segmentEnd)
		if err != nil {
			return 0, fmt.Errorf("billing: usage in %s: %w", monthStart.Format(time.RFC3339), err)
		}
		before := api.BillableMBSeconds(acct.Plan, prior)
		after := api.BillableMBSeconds(acct.Plan, prior+current)
		total += after - before
	}
	return total, nil
}

func sumUsage(ctx context.Context, store state.Store, accountID string, start, end time.Time) (int64, error) {
	if !start.Before(end) {
		return 0, nil
	}
	rows, err := store.UsageByHour(ctx, accountID, start, end)
	if err != nil {
		return 0, err
	}
	var total int64
	for _, row := range rows {
		total += row.MBSeconds
	}
	return total, nil
}

func utcMonthStart(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
}
