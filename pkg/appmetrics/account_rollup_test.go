package appmetrics

import "testing"

// Hunt #10: account dashboard windows past an hour read the recorded rollups,
// shorter ones the raw counters.
func TestAccountIncreaseQuery(t *testing.T) {
	const sel = `app=~"^(?:a1)$"`
	for _, tc := range []struct {
		counter AccountCounter
		by      string
		window  string
		want    string
	}{
		{AccountRequestCount, "app, class", "5m", `sum by (app, class)(increase(gateway_request_duration_seconds_count{app=~"^(?:a1)$"}[5m]))`},
		{AccountRequestCount, "app, class", "1h", `sum by (app, class)(increase(gateway_request_duration_seconds_count{app=~"^(?:a1)$"}[1h]))`},
		{AccountRequestCount, "app, class", "6h", `sum by (app, class)(sum_over_time(app_class:gateway_request_duration_seconds_count:increase5m{app=~"^(?:a1)$"}[6h]))`},
		{AccountRequestCount, "app, class", "24h", `sum by (app, class)(sum_over_time(app_class:gateway_request_duration_seconds_count:increase5m{app=~"^(?:a1)$"}[24h]))`},
		{AccountRequestCount, "app, class", "7d", `sum by (app, class)(sum_over_time(app_class:gateway_request_duration_seconds_count:increase1h{app=~"^(?:a1)$"}[7d]))`},
		{AccountRequestBuckets, "app, le", "15d", `sum by (app, le)(sum_over_time(app_class_le:gateway_request_duration_seconds_bucket:increase1h{app=~"^(?:a1)$"}[15d]))`},
		{AccountRequestBuckets, "app, le", "15m", `sum by (app, le)(increase(gateway_request_duration_seconds_bucket{app=~"^(?:a1)$"}[15m]))`},
	} {
		if got := AccountIncreaseQuery(tc.counter, tc.by, sel, tc.window); got != tc.want {
			t.Errorf("%s over %s:\n got %s\nwant %s", tc.counter.raw, tc.window, got, tc.want)
		}
	}
	for _, window := range Ranges() {
		if got := AccountIncreaseQuery(AccountRequestCount, "app", sel, window); got == "" {
			t.Errorf("no query for dashboard range %s", window)
		}
	}
}
