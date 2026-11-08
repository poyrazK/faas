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

// Minutes are kept 48 hours (amendment 3): past the read horizon the
// rollups answer only a window that starts on a whole hour the hourly tier
// holds; anything else falls back to raw rows rather than reading swept
// minutes.
func TestRollupCoverageReadsOnlyHoursPastTheMinuteHorizon(t *testing.T) {
	ctx := t.Context()
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	store := state.NewPgStore(pool)
	account, err := store.CreateAccount(ctx, "rollup-horizon@example.test", api.PlanScale)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "rollup-horizon"})
	if err != nil {
		t.Fatal(err)
	}
	appID := uuid.MustParse(app.ID)
	now := time.Now().UTC()
	minutesSince := now.Truncate(time.Hour).Add(-6 * 24 * time.Hour)
	hoursSince := now.Truncate(time.Hour).Add(-4 * 24 * time.Hour)
	if err := store.AddRequestAnalyticsRollups(ctx, nil, []state.RequestAnalyticsCoverage{{AppID: appID, CoveredSince: minutesSince}}); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE request_analytics_rollup_coverage SET hours_covered_since = $2 WHERE app_id = $1`, appID, hoursSince); err != nil {
		t.Fatal(err)
	}
	firstWholeHour := hoursSince.Add(time.Hour) // the tier's first hour after its coverage and the settle lag
	for _, tc := range []struct {
		name string
		from time.Time
		want bool
	}{
		{"recent window reads minutes", now.Add(-24 * time.Hour), true},
		{"old window on an hour the tier holds", firstWholeHour, true},
		{"old window before the tier's coverage", firstWholeHour.Add(-time.Hour), false},
		{"old window not on an hour", firstWholeHour.Add(time.Hour + 30*time.Minute), false},
	} {
		if _, ok := analyticsrollup.Covers(ctx, store, app.ID, tc.from); ok != tc.want {
			t.Errorf("%s: Covers(%s) = %v, want %v", tc.name, tc.from, ok, tc.want)
		}
	}
}

// A row holds its histogram as bigint, as integer once a later release
// writes that alone, or as both while the two releases run side by side;
// reads add the two columns bucket by bucket (amendment 3).
func TestRollupReadsAddBothHistogramColumns(t *testing.T) {
	ctx := t.Context()
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	store := state.NewPgStore(pool)
	account, err := store.CreateAccount(ctx, "rollup-hist-int4@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "rollup-hist-int4"})
	if err != nil {
		t.Fatal(err)
	}
	appID, accountID := uuid.MustParse(app.ID), uuid.MustParse(account.ID)
	minute := time.Now().UTC().Truncate(time.Minute).Add(-30 * time.Minute)
	deployment := uuid.New()

	// This release's writer: bigint histogram, 5 requests in bucket 0.
	old := state.RequestAnalyticsRollup{
		AppID: appID, AccountID: accountID, DeploymentID: deployment, BucketStart: minute,
		DimensionKind: "route", Dimension: "GET /", Method: "GET", Status: 200, Requests: 5, LatencyMaxMS: 9,
	}
	old.LatencyHist[0] = 5
	if err := store.AddRequestAnalyticsRollups(ctx, []state.RequestAnalyticsRollup{old}, []state.RequestAnalyticsCoverage{{AppID: appID, CoveredSince: minute.Add(-time.Hour)}}); err != nil {
		t.Fatal(err)
	}
	// A later release's writer: integer histogram alone, a second minute
	// with 3 requests in bucket 44 (overflow), as it would insert them.
	if _, err := pool.Exec(ctx, `
		INSERT INTO request_analytics_minutes (app_id, bucket_start, dimension_kind, dimension, method, status, deployment_id,
		    account_id, requests, error_requests, cold_boots, request_time_ms, latency_max_ms, latency_hist, latency_hist_int4)
		VALUES ($1, $2, 'route', 'GET /', 'GET', 200, $3, $4, 3, 0, 0, 0, 90000, NULL,
		    (SELECT array_agg(CASE WHEN i = 45 THEN 3 ELSE 0 END ORDER BY i) FROM generate_series(1, 45) i))`,
		appID, minute.Add(time.Minute), deployment, accountID); err != nil {
		t.Fatal(err)
	}
	// This release adding to that integer-only row starts its bigint
	// histogram from zeros: 2 more requests in bucket 0.
	more := old
	more.BucketStart, more.Requests, more.LatencyHist = minute.Add(time.Minute), 2, [api.RequestAnalyticsLatencyBuckets]int64{0: 2}
	if err := store.AddRequestAnalyticsRollups(ctx, []state.RequestAnalyticsRollup{more}, nil); err != nil {
		t.Fatal(err)
	}

	groups, err := analyticsrollup.Read(ctx, store, analyticsrollup.Query{
		AppID: app.ID, AccountID: account.ID, From: minute.Add(-time.Minute), Until: minute.Add(2 * time.Minute), GroupBy: "route",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 1 {
		t.Fatalf("groups = %+v, want one route group", groups)
	}
	g := groups[0]
	if g.Requests != 10 || g.LatencyHist[0] != 7 || g.LatencyHist[api.RequestAnalyticsLatencyBuckets-1] != 3 {
		t.Fatalf("group = %d requests, hist[0] %d, overflow %d; want 10, 7 and 3", g.Requests, g.LatencyHist[0], g.LatencyHist[api.RequestAnalyticsLatencyBuckets-1])
	}
}
