package billing

import (
	"context"
	"fmt"
	"time"

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
// provider pusher applies it hour by hour (ADR-1162, ADR-20261030093512):
// the range is the month's billed compute at its end less the billed compute
// at its start, both from state.MonthBilledComputeMBSeconds, the one
// implementation the pusher's spend cap and the cap gate also use
// (ADR-20261031161842). The result equals the sum of the pusher's hourly
// deliveries before any spend-cap clamp. acct.Plan is the fallback for stores
// or months without recorded activations.
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
		usageBefore := func(at time.Time) (int64, error) {
			return sumUsage(ctx, store, acct.ID, monthStart, at)
		}
		billedAt := func(at time.Time) (int64, error) {
			usage, err := usageBefore(at)
			if err != nil {
				return 0, err
			}
			return state.MonthBilledComputeMBSeconds(acct.Plan, snapshots, at, usage, usageBefore)
		}
		before, err := billedAt(segmentStart)
		if err != nil {
			return 0, fmt.Errorf("billing: usage before %s: %w", segmentStart.Format(time.RFC3339), err)
		}
		after, err := billedAt(segmentEnd)
		if err != nil {
			return 0, fmt.Errorf("billing: usage in %s: %w", monthStart.Format(time.RFC3339), err)
		}
		total += after - before
	}
	return total, nil
}

// MonthComputeOverage is a month's compute allowance and the compute it is
// billed beyond that allowance, both in mb_seconds.
type MonthComputeOverage struct {
	IncludedMBSeconds int64
	BilledMBSeconds   int64
}

// ComputeOverageForMonth returns what acct's month containing month is billed
// for compute, given usage, the month's usage so far (the whole month once it
// has ended). The month in progress reads the figure the spend cap and the
// billing page read (state.CurrentMonthBilledComputeMBSeconds); an ended month
// is billed through its end. The allowance follows the month's recorded plan
// history; a month without one falls back to acct.Plan. The usage pages show
// this instead of usage above today's plan allowance, so they agree with the
// bill (ADR-20261101113742).
func ComputeOverageForMonth(ctx context.Context, store UsageRangeStore, acct state.Account, month, now time.Time, usage int64) (MonthComputeOverage, error) {
	if store == nil {
		return MonthComputeOverage{}, fmt.Errorf("billing: usage range store is nil")
	}
	monthStart := utcMonthStart(month)
	monthEnd := monthStart.AddDate(0, 1, 0)
	now = now.UTC()
	if !now.After(monthStart) {
		return MonthComputeOverage{}, nil
	}
	var snapshots []state.FinancialPriceSnapshot
	if record, ok := store.(state.FinancialStore); ok {
		var err error
		snapshots, err = record.ListFinancialPriceSnapshots(ctx, acct.ID, monthStart)
		if err != nil {
			return MonthComputeOverage{}, fmt.Errorf("billing: plan history for %s: %w", monthStart.Format("2006-01"), err)
		}
	}
	usageBefore := func(at time.Time) (int64, error) {
		return sumUsage(ctx, store, acct.ID, monthStart, at)
	}
	if now.Before(monthEnd) {
		billed, err := state.CurrentMonthBilledComputeMBSeconds(acct.Plan, snapshots, now, usage, usageBefore)
		if err != nil {
			return MonthComputeOverage{}, fmt.Errorf("billing: billed compute for %s: %w", monthStart.Format("2006-01"), err)
		}
		return MonthComputeOverage{IncludedMBSeconds: state.CurrentMonthComputeIncludedMBSeconds(acct.Plan, snapshots, now), BilledMBSeconds: billed}, nil
	}
	billed, err := state.MonthBilledComputeMBSeconds(acct.Plan, snapshots, monthEnd, usage, usageBefore)
	if err != nil {
		return MonthComputeOverage{}, fmt.Errorf("billing: billed compute for %s: %w", monthStart.Format("2006-01"), err)
	}
	return MonthComputeOverage{IncludedMBSeconds: state.MonthComputeIncludedMBSeconds(acct.Plan, snapshots, monthEnd), BilledMBSeconds: billed}, nil
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
