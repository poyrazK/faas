package billing

import (
	"context"
	"fmt"
	"sort"
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
//
// The allowance follows the month's recorded plan history exactly as the
// provider pusher applies it hour by hour (ADR-1162): each UTC hour uses the
// largest grant activated before the hour ends. A segment is split at the
// hours where that grant changes, so the result equals the sum of the
// pusher's hourly deliveries before any spend-cap clamp. acct.Plan is the
// fallback for stores or months without recorded activations.
func OverageMBSecondsForRange(ctx context.Context, store UsageRangeStore, acct state.Account, start, end time.Time) (int64, error) {
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

		var snapshots []state.FinancialPriceSnapshot
		if record, ok := store.(state.FinancialStore); ok {
			var err error
			snapshots, err = record.ListFinancialPriceSnapshots(ctx, acct.ID, monthStart)
			if err != nil {
				return 0, fmt.Errorf("billing: plan history for %s: %w", monthStart.Format(time.RFC3339), err)
			}
		}
		prior, err := sumUsage(ctx, store, acct.ID, monthStart, segmentStart)
		if err != nil {
			return 0, fmt.Errorf("billing: usage before %s: %w", monthStart.Format(time.RFC3339), err)
		}
		from := segmentStart
		for _, to := range append(grantBoundaries(snapshots, segmentStart, segmentEnd), segmentEnd) {
			current, err := sumUsage(ctx, store, acct.ID, from, to)
			if err != nil {
				return 0, fmt.Errorf("billing: usage in %s: %w", monthStart.Format(time.RFC3339), err)
			}
			included := state.MonthComputeIncludedMBSeconds(acct.Plan, snapshots, from.Truncate(time.Hour).Add(time.Hour))
			total += api.BillableMBSecondsAbove(included, prior+current) - api.BillableMBSecondsAbove(included, prior)
			prior += current
			from = to
		}
	}
	return total, nil
}

// grantBoundaries returns the sorted UTC hours strictly inside (start, end)
// from which a recorded compute activation first applies. An activation at
// instant e counts for every hour that ends after e, so it starts applying
// at e truncated to its hour.
func grantBoundaries(snapshots []state.FinancialPriceSnapshot, start, end time.Time) []time.Time {
	seen := map[time.Time]bool{}
	var out []time.Time
	for _, s := range snapshots {
		if s.Price.Meter != "compute" {
			continue
		}
		boundary := s.EffectiveFrom.UTC().Truncate(time.Hour)
		if boundary.After(start) && boundary.Before(end) && !seen[boundary] {
			seen[boundary] = true
			out = append(out, boundary)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Before(out[j]) })
	return out
}

// UsageRangeStore reads an account's hourly usage over a UTC range. A store
// that also implements state.FinancialStore supplies the plan history.
type UsageRangeStore interface {
	UsageByHour(ctx context.Context, accountID string, start, end time.Time) ([]state.Usage, error)
}

func sumUsage(ctx context.Context, store UsageRangeStore, accountID string, start, end time.Time) (int64, error) {
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
