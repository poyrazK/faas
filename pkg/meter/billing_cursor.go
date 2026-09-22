package meter

import (
	"errors"
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

func (c *overageCursor) advance(hour time.Time, quantity int64, sum func(time.Time, time.Time) (int64, error)) (before, after int64, err error) {
	if hour.Before(c.nextHour) {
		return 0, 0, errors.New("meter: billing windows are not in increasing hour order")
	}
	before = c.rawBefore
	if c.nextHour.Before(hour) {
		gap, err := sum(c.nextHour, hour)
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
	c.nextHour = hour.Add(time.Hour)
	return before, after, nil
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
