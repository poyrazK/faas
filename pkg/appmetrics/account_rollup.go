package appmetrics

import "fmt"

// AccountCounter is a gateway counter the account dashboard sums per app,
// with the recording rules that pre-aggregate it by the labels the dashboard
// groups on (faas_gateway_account_rollups_5m and _1h in faas.rules.yml).
type AccountCounter struct {
	raw, rollup5m, rollup1h string
}

var (
	// AccountRequestCount carries the app and class labels.
	AccountRequestCount = AccountCounter{
		raw:      "gateway_request_duration_seconds_count",
		rollup5m: "app_class:gateway_request_duration_seconds_count:increase5m",
		rollup1h: "app_class:gateway_request_duration_seconds_count:increase1h",
	}
	// AccountRequestBuckets carries the app, class and le labels.
	AccountRequestBuckets = AccountCounter{
		raw:      "gateway_request_duration_seconds_bucket",
		rollup5m: "app_class_le:gateway_request_duration_seconds_bucket:increase5m",
		rollup1h: "app_class_le:gateway_request_duration_seconds_bucket:increase1h",
	}
)

// AccountIncreaseQuery returns the increase of c over window, summed by the
// labels in by, for the series selector matches. Windows up to an hour read
// the raw counter. Longer windows sum the 5m rollup (6h, 24h) or the hourly
// rollup (7d, 15d): over 24h the raw latency buckets took 17 s on the
// production-us control plane against apid's 5 s budget, so every account
// dashboard window past an hour came back degraded (hunt #10). A rollup lags
// the newest data by up to its interval and covers only the time since its
// rules started.
func AccountIncreaseQuery(c AccountCounter, by, selector, window string) string {
	switch window {
	case "6h", "24h":
		return fmt.Sprintf(`sum by (%s)(sum_over_time(%s{%s}[%s]))`, by, c.rollup5m, selector, window)
	case "7d", "15d":
		return fmt.Sprintf(`sum by (%s)(sum_over_time(%s{%s}[%s]))`, by, c.rollup1h, selector, window)
	default:
		return fmt.Sprintf(`sum by (%s)(increase(%s{%s}[%s]))`, by, c.raw, selector, window)
	}
}
