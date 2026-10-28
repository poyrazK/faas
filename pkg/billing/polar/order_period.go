package polar

import "time"

// polarOrderPeriod is the service period a Polar order bills. Polar does not
// put a period on the order itself: each line carries start_timestamp and
// end_timestamp. The recurring plan line spans the subscription period, while
// a metered usage line can have start == end, so lines with a real span win
// over zero-length ones. Top-level period fields are still honoured first for
// payloads that carry them. A zero result means the order has no period.
func polarOrderPeriod(order map[string]any) (time.Time, time.Time) {
	start := firstTime(order, "period_start", "billing_period_start")
	end := firstTime(order, "period_end", "billing_period_end")
	if start.IsZero() {
		start = nestedTime(order, "billing_period", "starts_at")
	}
	if end.IsZero() {
		end = nestedTime(order, "billing_period", "ends_at")
	}
	if !start.IsZero() && !end.IsZero() {
		return start, end
	}
	items, ok := order["items"].([]any)
	if !ok || len(items) == 0 {
		return time.Time{}, time.Time{}
	}
	var spanStart, spanEnd, anyStart, anyEnd time.Time
	for _, raw := range items {
		item, ok := raw.(map[string]any)
		if !ok {
			return time.Time{}, time.Time{}
		}
		lineStart, lineEnd := timeValue(item["start_timestamp"]), timeValue(item["end_timestamp"])
		if lineStart.IsZero() || lineEnd.IsZero() || lineEnd.Before(lineStart) {
			return time.Time{}, time.Time{}
		}
		anyStart, anyEnd = earlier(anyStart, lineStart), later(anyEnd, lineEnd)
		if lineEnd.After(lineStart) {
			spanStart, spanEnd = earlier(spanStart, lineStart), later(spanEnd, lineEnd)
		}
	}
	if !spanStart.IsZero() {
		return spanStart, spanEnd
	}
	return anyStart, anyEnd
}

func earlier(current, candidate time.Time) time.Time {
	if current.IsZero() || candidate.Before(current) {
		return candidate
	}
	return current
}

func later(current, candidate time.Time) time.Time {
	if current.IsZero() || candidate.After(current) {
		return candidate
	}
	return current
}
