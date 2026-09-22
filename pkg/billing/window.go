package billing

import "time"

// CompletedUsageWindow returns complete UTC hours ending before at. A
// fractional-hour lookback is rounded down: older partial hours must not be
// delivered under an immutable whole-hour idempotency key or compared with a
// whole-hour provider event. A lookback shorter than one hour is empty.
func CompletedUsageWindow(at time.Time, lookback time.Duration) (start, end time.Time) {
	end = at.UTC().Truncate(time.Hour)
	return end.Add(-max(lookback, 0).Truncate(time.Hour)), end
}
