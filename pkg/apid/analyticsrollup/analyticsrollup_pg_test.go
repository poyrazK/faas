//go:build !no_pg

// adr: 20261103113952

package analyticsrollup_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/analyticsrollup"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestRollupReadsGroupFoldAndSplitByHour(t *testing.T) {
	ctx := t.Context()
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	store := state.NewPgStore(pool)
	account, err := store.CreateAccount(ctx, "rollup-reads@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "rollup-reads"})
	if err != nil {
		t.Fatal(err)
	}
	appID, accountID := uuid.MustParse(app.ID), uuid.MustParse(account.ID)
	deployments := []uuid.UUID{uuid.New(), uuid.New()}
	hour := time.Now().UTC().Truncate(time.Hour).Add(-2 * time.Hour)
	row := func(minute time.Time, kind, dimension, method string, status int16, deployment uuid.UUID, requests int64, bucket int) state.RequestAnalyticsRollup {
		r := state.RequestAnalyticsRollup{
			AppID: appID, AccountID: accountID, DeploymentID: deployment, BucketStart: minute,
			DimensionKind: kind, Dimension: dimension, Method: method, Status: status,
			Requests: requests, RequestTimeMS: requests * 10, LatencyMaxMS: int32(api.RequestAnalyticsLatencyBoundsMS[bucket]),
		}
		if status >= 400 {
			r.ErrorRequests = requests
		}
		r.LatencyHist[bucket] = requests
		return r
	}
	rows := []state.RequestAnalyticsRollup{
		// Hour 1: /a 200 on two deployments and /a 500; /b 200.
		row(hour.Add(5*time.Minute), "route", "GET /a", "GET", 200, deployments[0], 60, 0),
		row(hour.Add(5*time.Minute), "route", "GET /a", "GET", 200, deployments[1], 30, 1),
		row(hour.Add(6*time.Minute), "route", "GET /a", "GET", 500, deployments[0], 10, 9),
		row(hour.Add(7*time.Minute), "route", "GET /b", "GET", 200, deployments[0], 5, 4),
		// Hour 3: /c, and a country the writer already folded.
		row(hour.Add(2*time.Hour+time.Minute), "route", "POST /c", "POST", 201, deployments[0], 1, 43),
		row(hour.Add(5*time.Minute), "country", "DE", "", 200, deployments[0], 90, 0),
		row(hour.Add(5*time.Minute), "country", "__other__", "", 200, deployments[0], 4, 0),
		row(hour.Add(6*time.Minute), "country", "FR", "", 500, deployments[0], 12, 0),
	}
	if err := store.AddRequestAnalyticsRollups(ctx, rows, []state.RequestAnalyticsCoverage{{AppID: appID, CoveredSince: hour}}); err != nil {
		t.Fatal(err)
	}
	from, until := hour, hour.Add(3*time.Hour)
	q := analyticsrollup.Query{AppID: app.ID, AccountID: account.ID, From: from, Until: until, GroupBy: "route", Limit: 1}

	// Top one route, the rest folded; the summary adds up every request.
	groups, err := analyticsrollup.Read(ctx, store, q)
	if err != nil {
		t.Fatal(err)
	}
	byDimension := analyticsrollup.ByDimension(groups, nil)
	if len(byDimension) != 2 || byDimension[0].Dimension != "GET /a" || byDimension[0].Requests != 100 ||
		byDimension[0].ErrorRequests != 10 || byDimension[0].RequestTimeMs != 1000 ||
		byDimension[1].Dimension != analyticsrollup.OtherDimension || byDimension[1].Requests != 6 {
		t.Fatalf("route groups = %+v, want GET /a with 100 requests then __other__ with 6", byDimension)
	}
	// /a: 60 at <=10 ms, 30 at <=20 ms, 10 at <=100 ms.
	if byDimension[0].P50Ms != 10 || byDimension[0].P95Ms != 100 {
		t.Fatalf("GET /a p50/p95 = %d/%d, want 10/100", byDimension[0].P50Ms, byDimension[0].P95Ms)
	}
	summary := analyticsrollup.Summary(groups)
	if summary.Requests != 106 || summary.ErrorRequests != 10 || summary.P99Ms != 100 {
		t.Fatalf("summary = %+v, want 106 requests, 10 errors, p99 100", summary)
	}

	// Status groups come from the route rows.
	q.GroupBy, q.Limit = "status", 10
	groups, err = analyticsrollup.Read(ctx, store, q)
	if err != nil {
		t.Fatal(err)
	}
	statuses := map[string]int64{}
	for _, g := range groups {
		statuses[g.Dimension] += g.Requests
	}
	if statuses["200"] != 95 || statuses["500"] != 10 || statuses["201"] != 1 {
		t.Fatalf("status groups = %v", statuses)
	}

	// The writer's __other__ and the reader's own fold merge into one group.
	q.GroupBy, q.Limit = "country", 1
	groups, err = analyticsrollup.Read(ctx, store, q)
	if err != nil {
		t.Fatal(err)
	}
	byDimension = analyticsrollup.ByDimension(groups, nil)
	if len(byDimension) != 2 || byDimension[0].Dimension != "DE" || byDimension[1].Dimension != analyticsrollup.OtherDimension || byDimension[1].Requests != 16 {
		t.Fatalf("country groups = %+v, want DE then one __other__ of 16", byDimension)
	}

	// Hourly series zero-fill the empty hour; the route filter keeps /a only.
	q = analyticsrollup.Query{AppID: app.ID, AccountID: account.ID, From: from, Until: until, GroupBy: "route", Hourly: true}
	groups, err = analyticsrollup.Read(ctx, store, q)
	if err != nil {
		t.Fatal(err)
	}
	series := analyticsrollup.Timeseries(groups, from, until)
	got := ""
	for _, p := range series {
		got += fmt.Sprintf("%d,", p.Requests)
	}
	if got != "105,0,1," || series[2].P50Ms != 60000 {
		t.Fatalf("hourly requests = %s p50 of hour 3 = %d, want 105,0,1, and 60000", got, series[2].P50Ms)
	}
	q.Route, q.Method, q.GroupBy, q.Limit = "GET /a", "GET", "status", 10
	groups, err = analyticsrollup.Read(ctx, store, q)
	if err != nil {
		t.Fatal(err)
	}
	grouped := analyticsrollup.TimeseriesGrouped(groups, from, until)
	if len(grouped) != 6 || grouped[0].Dimension != "200" || grouped[0].Requests != 90 || grouped[3].Dimension != "500" || grouped[3].Requests != 10 {
		t.Fatalf("GET /a status series = %+v", grouped)
	}

	// Coverage starts at the first minute; windows from before it plus the
	// settle lag stay on raw rows.
	if _, ok := analyticsrollup.Covers(ctx, store, app.ID, hour.Add(api.RequestAnalyticsSettleLag)); !ok {
		t.Fatal("rollups do not cover a window that starts after the settle lag")
	}
	if _, ok := analyticsrollup.Covers(ctx, store, app.ID, hour); ok {
		t.Fatal("rollups cover a window that starts in their first minute")
	}
	if _, ok := analyticsrollup.Covers(ctx, store, uuid.NewString(), hour.Add(time.Hour)); ok {
		t.Fatal("rollups cover an app that has none")
	}
}
