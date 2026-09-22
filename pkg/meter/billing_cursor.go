package meter

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"
)

// overageCursor tracks raw calendar-month usage, not just pending deliveries.
// Delivered hours disappear from the pending scan but must still consume the
// included allowance and spending cap. Contiguous windows need no extra read;
// gaps are filled from the canonical usage ledger.
type overageCursor struct {
	rawBefore int64
	nextHour  time.Time
}

// start may be inside the first hour when a provider identity was activated
// mid-hour. Its prefix still consumes the allowance, but is not sent again.
func (c *overageCursor) advance(start time.Time, quantity int64, sum func(time.Time, time.Time) (int64, error)) (before, after int64, err error) {
	if start.Before(c.nextHour) {
		return 0, 0, errors.New("meter: billing windows are not in increasing hour order")
	}
	before = c.rawBefore
	if c.nextHour.Before(start) {
		gap, err := sum(c.nextHour, start)
		if err != nil {
			return 0, 0, err
		}
		before, err = addUsageQuantity(before, gap)
		if err != nil {
			return 0, 0, err
		}
	}
	after, err = addUsageQuantity(before, quantity)
	if err != nil {
		return 0, 0, err
	}
	// Failed reads/arithmetic must leave the cursor untouched so the next
	// pending window cannot silently skip the failed window's usage.
	c.rawBefore = after
	c.nextHour = start.UTC().Truncate(time.Hour).Add(time.Hour)
	return before, after, nil
}

// startOverageCursor reads the same identity boundary used by the pending
// window query. Only the first pending hour can contain a filtered prefix;
// subsequent contiguous hours keep the no-extra-read fast path.
func (p *Pusher) startOverageCursor(ctx context.Context, accountID string, usageStart, hour time.Time) (overageCursor, time.Time, error) {
	identity, err := p.store.BillingIdentity(ctx, accountID, providerOpsFor(p.pusher).opLabel)
	if err != nil {
		return overageCursor{}, time.Time{}, fmt.Errorf("load billing usage boundary: %w", err)
	}
	start := hour
	if identity.BillingFrom.After(start) {
		start = identity.BillingFrom.UTC()
	}
	if !start.Before(hour.Add(time.Hour)) {
		return overageCursor{}, time.Time{}, errors.New("meter: pending hour precedes provider activation")
	}
	return overageCursor{nextHour: usageStart}, start, nil
}

func addUsageQuantity(total, quantity int64) (int64, error) {
	if total < 0 || quantity < 0 {
		return 0, errors.New("meter: negative usage quantity")
	}
	if quantity > math.MaxInt64-total {
		return 0, errors.New("meter: usage quantity overflow")
	}
	return total + quantity, nil
}
