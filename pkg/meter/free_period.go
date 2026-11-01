package meter

import (
	"context"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// freePeriod is when a Free account's current Free period began in month.
// paid is false when the account held no paid plan that month.
type freePeriod struct {
	month, since time.Time
	paid         bool
}

// freePeriodUsageGB is the usage the Free quota counts (ADR-20261031090658).
// After a paid plan ends during the month, usage on it was billed on that
// plan's terms and does not count toward the Free allowance: only usage since
// the account returned to Free does. A read failure keeps the month's usage,
// the stricter count.
func (l *Loop) freePeriodUsageGB(ctx context.Context, acct state.Account, now time.Time, monthGB float64) float64 {
	period, err := l.freePeriodStart(ctx, acct, now)
	if err != nil {
		l.log.Warn("meter: free period read failed", "account", acct.ID, "err", err)
		return monthGB
	}
	if !period.paid {
		return monthGB
	}
	rows, err := l.store.UsageByHour(ctx, acct.ID, period.since, now.UTC())
	if err != nil {
		l.log.Warn("meter: free period usage read failed", "account", acct.ID, "err", err)
		return monthGB
	}
	return MonthlyUsageGB(rows)
}

// freePeriodStart reads the account's recorded plan history for the month of
// now, once per account and month while the account stays on Free.
func (l *Loop) freePeriodStart(ctx context.Context, acct state.Account, now time.Time) (freePeriod, error) {
	at := now.UTC()
	month := time.Date(at.Year(), at.Month(), 1, 0, 0, 0, 0, time.UTC)
	if cached, ok := l.freePeriods[acct.ID]; ok && cached.month.Equal(month) {
		return cached, nil
	}
	period := freePeriod{month: month}
	if record, ok := l.store.(state.FinancialStore); ok {
		snapshots, err := record.ListFinancialPriceSnapshots(ctx, acct.ID, month)
		if err != nil {
			return freePeriod{}, err
		}
		period.since, period.paid = freeSince(snapshots, at)
	}
	if l.freePeriods == nil {
		l.freePeriods = map[string]freePeriod{}
	}
	l.freePeriods[acct.ID] = period
	return period, nil
}

// freeSince returns when the account last moved from a paid plan to Free in
// the month the compute activations cover, and whether it held a paid plan
// at all. A paid plan that is still the latest recorded one means the move
// to Free has not been recorded yet (meterd records it within a minute), so
// the Free period starts now.
func freeSince(snapshots []state.FinancialPriceSnapshot, now time.Time) (time.Time, bool) {
	var compute []state.FinancialPriceSnapshot
	for _, s := range snapshots {
		if s.Price.Meter == string(state.BillingMeterCompute) && !s.EffectiveFrom.After(now) {
			compute = append(compute, s)
		}
	}
	slices.SortStableFunc(compute, func(a, b state.FinancialPriceSnapshot) int {
		if c := a.EffectiveFrom.Compare(b.EffectiveFrom); c != 0 {
			return c
		}
		return a.RecordedAt.Compare(b.RecordedAt)
	})
	var since time.Time
	paid := false
	for _, s := range compute {
		switch {
		case s.Plan != api.PlanFree:
			paid, since = true, time.Time{}
		case paid && since.IsZero():
			since = s.EffectiveFrom.UTC()
		}
	}
	if paid && since.IsZero() {
		since = now
	}
	return since, paid
}
