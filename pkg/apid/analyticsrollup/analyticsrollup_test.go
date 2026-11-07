package analyticsrollup_test

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/analyticsrollup"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func TestByDimensionTakesSampledMeasuresFromMatchingRawGroups(t *testing.T) {
	hist := make([]int64, api.RequestAnalyticsLatencyBuckets)
	hist[2] = 7
	groups := []analyticsrollup.Group{
		{Dimension: "GET /b", Method: "GET", Requests: 7, LatencyMaxMS: 25, LatencyHist: hist},
		{Dimension: "GET /a", Method: "GET", Requests: 7, LatencyMaxMS: 25, LatencyHist: hist},
	}
	sampled := []sqlc.RequestTelemetryAnalyticsByDimensionRow{
		{Dimension: "GET /a", Method: "GET", Requests: 3, GuestCpuAvgMs: pgtype.Int4{Int32: 4, Valid: true}},
		{Dimension: "GET /gone", Method: "GET", Requests: 1},
	}
	rows := analyticsrollup.ByDimension(groups, sampled)
	if len(rows) != 2 || rows[0].Dimension != "GET /a" || rows[1].Dimension != "GET /b" {
		t.Fatalf("order = %+v, want ties broken by dimension", rows)
	}
	if rows[0].Requests != 7 || !rows[0].GuestCpuAvgMs.Valid || rows[0].GuestCpuAvgMs.Int32 != 4 || rows[1].GuestCpuAvgMs.Valid {
		t.Fatalf("rows = %+v, want rollup counts and GET /a's sampled CPU only", rows)
	}
	// Every request is at <=30 ms but none above 25 ms.
	if rows[0].P50Ms != 25 {
		t.Fatalf("p50 = %d, want the 25 ms maximum, not the 30 ms bound", rows[0].P50Ms)
	}
}

func TestHistPercentileAndHours(t *testing.T) {
	hist := make([]int64, api.RequestAnalyticsLatencyBuckets)
	hist[0], hist[api.RequestAnalyticsLatencyBuckets-1] = 99, 1
	if got := state.RequestAnalyticsHistPercentile(hist, 90_000, 0.99); got != 10 {
		t.Fatalf("p99 = %d, want 10", got)
	}
	if got := state.RequestAnalyticsHistPercentile(hist, 90_000, 0.999); got != 90_000 {
		t.Fatalf("p99.9 = %d, want the overflow bucket's 90000 maximum", got)
	}
	if got := state.RequestAnalyticsHistPercentile(make([]int64, api.RequestAnalyticsLatencyBuckets), 0, 0.5); got != 0 {
		t.Fatalf("empty p50 = %d", got)
	}
	from := time.Date(2026, 11, 3, 10, 30, 0, 0, time.UTC)
	if hours := analyticsrollup.Hours(from, from.Add(90*time.Minute)); len(hours) != 2 || !hours[0].Equal(from.Truncate(time.Hour)) {
		t.Fatalf("hours = %v, want 10:00 and 11:00", hours)
	}
	if !analyticsrollup.SupportsFilter("country", "", "") || analyticsrollup.SupportsFilter("country", "GET /a", "") || !analyticsrollup.SupportsFilter("status", "GET /a", "GET") {
		t.Fatal("route filters are answerable only by route and status groups")
	}
}

func TestHourRangeTakesWholeCoveredHoursOnly(t *testing.T) {
	at := func(h, m int) time.Time { return time.Date(2026, 11, 3, h, m, 0, 0, time.UTC) }
	cases := []struct {
		name               string
		from, until, since time.Time
		has                bool
		wantFrom, wantTo   time.Time
	}{
		{"no hourly coverage", at(1, 0), at(9, 0), time.Time{}, false, at(9, 0), at(9, 0)},
		{"partial edges stay on minutes", at(1, 30), at(9, 15), at(0, 0), true, at(2, 0), at(9, 0)},
		{"aligned window", at(2, 0), at(9, 0), at(0, 0), true, at(2, 0), at(9, 0)},
		{"coverage plus settle lag starts the tier", at(1, 0), at(9, 0), at(3, 58), true, at(4, 0), at(9, 0)},
		{"coverage on the hour still waits the lag", at(1, 0), at(9, 0), at(4, 0), true, at(5, 0), at(9, 0)},
		{"window inside one hour", at(4, 10), at(4, 50), at(0, 0), true, at(4, 50), at(4, 50)},
		{"no whole hour across a boundary", at(4, 30), at(5, 30), at(0, 0), true, at(5, 30), at(5, 30)},
	}
	for _, tc := range cases {
		gotFrom, gotTo := analyticsrollup.HourRange(tc.from, tc.until, tc.since, tc.has)
		if !gotFrom.Equal(tc.wantFrom) || !gotTo.Equal(tc.wantTo) {
			t.Errorf("%s: HourRange = [%s, %s), want [%s, %s)", tc.name, gotFrom, gotTo, tc.wantFrom, tc.wantTo)
		}
	}
}
