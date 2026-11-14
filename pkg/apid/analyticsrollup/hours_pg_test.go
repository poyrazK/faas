//go:build !no_pg

// adr: 20261103113952

package analyticsrollup_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/analyticsrollup"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

// A long window reads whole hours from the hourly tier and its partial
// edges from minutes, and answers exactly what the minutes alone do. An app
// whose hourly coverage is unknown (written before the tier) reads minutes.
func TestRollupHoursTierMatchesMinutesAcrossLongWindows(t *testing.T) {
	ctx := t.Context()
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	store := state.NewPgStore(pool)
	account, err := store.CreateAccount(ctx, "rollup-hours@example.test", api.PlanScale)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "rollup-hours"})
	if err != nil {
		t.Fatal(err)
	}
	appID, accountID := uuid.MustParse(app.ID), uuid.MustParse(account.ID)
	deployments := []uuid.UUID{uuid.New(), uuid.New()}
	start := time.Now().UTC().Truncate(time.Hour).Add(-50 * time.Hour)

	// One row every 17 minutes over 49 hours: two routes, two statuses, two
	// deployments, latencies spread over the histogram.
	var rows []state.RequestAnalyticsRollup
	for i := 0; i*17 < 49*60; i++ {
		route, status := "GET /a", int16(200)
		if i%3 == 0 {
			route = "GET /b"
		}
		if i%5 == 0 {
			status = 503
		}
		bucket := i % len(api.RequestAnalyticsLatencyBoundsMS)
		r := state.RequestAnalyticsRollup{
			AppID: appID, AccountID: accountID, DeploymentID: deployments[i%2],
			BucketStart:   start.Add(time.Duration(i*17) * time.Minute),
			DimensionKind: "route", Dimension: route, Method: "GET", Status: status,
			Requests: int64(i%7 + 1), RequestTimeMS: int64(i),
			LatencyMaxMS: int32(api.RequestAnalyticsLatencyBoundsMS[bucket]),
		}
		if status >= 400 {
			r.ErrorRequests = r.Requests
		}
		r.LatencyHist[bucket] = r.Requests
		rows = append(rows, r)
	}
	// Flush in batches, as the aggregator does; same-hour rows from
	// different flushes add up in the hourly tier.
	for i := 0; i < len(rows); i += 40 {
		var coverage []state.RequestAnalyticsCoverage
		if i == 0 {
			coverage = []state.RequestAnalyticsCoverage{{AppID: appID, CoveredSince: start}}
		}
		if err := store.AddRequestAnalyticsRollups(ctx, rows[i:min(i+40, len(rows))], coverage); err != nil {
			t.Fatal(err)
		}
	}

	from := start.Add(37 * time.Minute)
	until := start.Add(48*time.Hour + 23*time.Minute)
	var wantRequests, wantErrors int64
	for _, r := range rows {
		if !r.BucketStart.Before(from) && r.BucketStart.Before(until) {
			wantRequests += r.Requests
			wantErrors += r.ErrorRequests
		}
	}
	since, ok, err := store.RequestAnalyticsRollupHoursCoverage(ctx, appID)
	if err != nil || !ok || !since.Equal(start) {
		t.Fatalf("hours coverage = %s, %v, %v; want %s", since, ok, err, start)
	}
	if hf, hu := analyticsrollup.HourRange(from, until, since, ok); !hf.Equal(start.Add(time.Hour)) || !hu.Equal(start.Add(48*time.Hour)) {
		t.Fatalf("hour range = [%s, %s), want the 47 whole hours", hf, hu)
	}

	read := func() (analyticsrollup.Group, []analyticsrollup.Group, map[analyticsrollup.RouteDeploymentKey]int64) {
		t.Helper()
		q := analyticsrollup.Query{AppID: app.ID, AccountID: account.ID, From: from, Until: until, GroupBy: "route"}
		groups, err := analyticsrollup.Read(ctx, store, q)
		if err != nil {
			t.Fatal(err)
		}
		q.GroupBy, q.Hourly = "status", true
		series, err := analyticsrollup.Read(ctx, store, q)
		if err != nil {
			t.Fatal(err)
		}
		split, err := analyticsrollup.RouteDeployments(ctx, store, app.ID, account.ID, from, until)
		if err != nil {
			t.Fatal(err)
		}
		total := analyticsrollup.Group{LatencyHist: make([]int64, api.RequestAnalyticsLatencyBuckets)}
		for _, g := range groups {
			total.Requests += g.Requests
			total.ErrorRequests += g.ErrorRequests
			total.RequestTimeMS += g.RequestTimeMS
			for b, n := range g.LatencyHist {
				total.LatencyHist[b] += n
			}
		}
		return total, series, split
	}
	withHours, hoursSeries, hoursSplit := read()
	if withHours.Requests != wantRequests || withHours.ErrorRequests != wantErrors {
		t.Fatalf("hours-tier totals = %d requests, %d errors; want %d, %d", withHours.Requests, withHours.ErrorRequests, wantRequests, wantErrors)
	}

	// The same window with the hourly tier unknown reads minutes only.
	if _, err := pool.Exec(ctx, `UPDATE request_analytics_rollup_coverage SET hours_covered_since = NULL WHERE app_id = $1`, appID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM request_analytics_hours WHERE app_id = $1`, appID); err != nil {
		t.Fatal(err)
	}
	minutesOnly, minutesSeries, minutesSplit := read()
	if minutesOnly.Requests != withHours.Requests || minutesOnly.ErrorRequests != withHours.ErrorRequests || minutesOnly.RequestTimeMS != withHours.RequestTimeMS {
		t.Fatalf("minute-only totals %+v differ from hours-tier totals %+v", minutesOnly, withHours)
	}
	for b := range withHours.LatencyHist {
		if withHours.LatencyHist[b] != minutesOnly.LatencyHist[b] {
			t.Fatalf("latency bucket %d: hours tier %d, minutes %d", b, withHours.LatencyHist[b], minutesOnly.LatencyHist[b])
		}
	}
	if len(hoursSeries) != len(minutesSeries) {
		t.Fatalf("hourly status series: %d groups with hours, %d with minutes", len(hoursSeries), len(minutesSeries))
	}
	for i := range hoursSeries {
		h, m := hoursSeries[i], minutesSeries[i]
		if !h.Bucket.Equal(m.Bucket) || h.Dimension != m.Dimension || h.Requests != m.Requests {
			t.Fatalf("series point %d: hours %+v, minutes %+v", i, h, m)
		}
	}
	if len(hoursSplit) != len(minutesSplit) {
		t.Fatalf("route deployment split: %v with hours, %v with minutes", hoursSplit, minutesSplit)
	}
	for k, n := range minutesSplit {
		if hoursSplit[k] != n {
			t.Fatalf("route deployment %+v: hours %d, minutes %d", k, hoursSplit[k], n)
		}
	}

	// The next flush records hourly coverage from its minute on.
	next := time.Now().UTC().Truncate(time.Minute)
	if err := store.AddRequestAnalyticsRollups(ctx, rows[:1], []state.RequestAnalyticsCoverage{{AppID: appID, CoveredSince: next}}); err != nil {
		t.Fatal(err)
	}
	if since, ok, err = store.RequestAnalyticsRollupHoursCoverage(ctx, appID); err != nil || !ok || !since.Equal(next) {
		t.Fatalf("hours coverage after the upgrade = %s, %v, %v; want %s", since, ok, err, next)
	}
}
