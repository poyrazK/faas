package state_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func TestCustomMetricConcurrentAdmissionAndObservationOrdering(t *testing.T) {
	store, ctx := pgStore(t)
	_, app, _ := seedLiveDeploy(t, store, ctx, "metric-quota", "metric-quota")
	now := time.Now().UTC().Truncate(time.Microsecond)
	for i := 0; i < api.MaxCustomMetricsPerApp-1; i++ {
		if err := store.PutCustomMetric(ctx, app, fmt.Sprintf("seed_%d", i), 1, now, api.MaxCustomMetricsPerApp); err != nil {
			t.Fatal(err)
		}
	}
	pool := *store.Pool()
	barrier, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = barrier.Rollback(context.WithoutCancel(ctx)) }()
	if _, err = barrier.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended('custom-metrics:' || $1::text,0))", app); err != nil {
		t.Fatal(err)
	}
	bounded, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	const n = 16
	monitor, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer monitor.Release()
	done := make(chan error, n)
	for i := 0; i < n; i++ {
		go func(i int) {
			name := fmt.Sprintf("new_%d", i)
			switch i % 3 {
			case 0:
				done <- store.PutCustomMetric(bounded, app, name, 1, now, api.MaxCustomMetricsPerApp)
			case 1:
				done <- store.AddCustomMetricCounter(bounded, app, name, 1, now, api.MaxCustomMetricsPerApp)
			default:
				done <- store.PutCustomMetricSeries(bounded, app, state.CustomMetricSeriesWrite{Name: name, Kind: state.CustomMetricKindGauge, Value: 1, ObservedAt: now, Labels: map[string]string{"region": "a"}, SeriesKey: "region=a"}, state.CustomMetricSeriesLimits{Names: api.MaxCustomMetricsPerApp, SeriesPerMetric: 10, SeriesPerApp: 100, GaugeFreshness: 5 * time.Minute})
			}
		}(i)
	}
	for {
		var waiting bool
		if err = monitor.QueryRow(bounded, "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE '-- name: LockCustomMetricAdmission%')").Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("write bypassed admission lock: %v", err)
		case <-bounded.Done():
			t.Fatal(bounded.Err())
		case <-time.After(5 * time.Millisecond):
		}
	}
	if err = barrier.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	accepted := 0
	for i := 0; i < n; i++ {
		err := <-done
		if err == nil {
			accepted++
		} else if !errors.Is(err, state.ErrCustomMetricLimit) {
			t.Fatal(err)
		}
	}
	rows, err := store.ListCustomMetrics(ctx, app)
	if err != nil || accepted != 1 || len(rows) != api.MaxCustomMetricsPerApp {
		t.Fatalf("accepted=%d rows=%d err=%v", accepted, len(rows), err)
	}
	for _, sample := range []struct {
		value float64
		at    time.Time
	}{{42, now.Add(time.Second)}, {3, now.Add(-10 * time.Minute)}} {
		if err = store.PutCustomMetric(ctx, app, "seed_0", sample.value, sample.at, api.MaxCustomMetricsPerApp); err != nil {
			t.Fatal(err)
		}
	}
	rows, err = store.ListCustomMetrics(ctx, app)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if r.Name == "seed_0" && (r.Value != 42 || !r.ObservedAt.Equal(now.Add(time.Second))) {
			t.Fatalf("new observation overwritten: %+v", r)
		}
	}
	if err = store.PutCustomMetric(ctx, app, "seed_1", 1, now.AddDate(1, 0, 0), api.MaxCustomMetricsPerApp); err != nil {
		t.Fatal(err)
	}
	fresh, err := store.ListFreshCustomMetrics(ctx, now.Add(-5*time.Minute), 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range fresh {
		if r.AppID == app && r.Name == "seed_1" {
			t.Fatal("future observation exported as fresh")
		}
	}
	if err = store.PutCustomMetric(ctx, app, "seed_1", 11, now, api.MaxCustomMetricsPerApp); err != nil {
		t.Fatal(err)
	}
	rows, err = store.ListCustomMetrics(ctx, app)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if r.Name == "seed_1" && (r.Value != 11 || !r.ObservedAt.Equal(now)) {
			t.Fatalf("legacy future value blocked recovery: %+v", r)
		}
	}
	lim := state.CustomMetricSeriesLimits{Names: api.MaxCustomMetricsPerApp, SeriesPerMetric: 10, SeriesPerApp: 100, GaugeFreshness: 5 * time.Minute}
	for _, sample := range []struct {
		value float64
		at    time.Time
	}{{42, now.Add(2 * time.Second)}, {3, now.Add(-10 * time.Minute)}} {
		if err := store.PutCustomMetricSeries(ctx, app, state.CustomMetricSeriesWrite{Name: "seed_2", SeriesKey: "region=a", Labels: map[string]string{"region": "a"}, Kind: state.CustomMetricKindGauge, Value: sample.value, ObservedAt: sample.at}, lim); err != nil {
			t.Fatal(err)
		}
	}
	series, err := store.ListCustomMetricSeries(ctx, app)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range series {
		if r.Name == "seed_2" && (r.Value != 42 || !r.ObservedAt.Equal(now.Add(2*time.Second))) {
			t.Fatalf("labelled observation overwritten: %+v", r)
		}
	}

}

func TestAnalyticsHistoricalCoverageSurvivesLaterLossAndNodeOutage(t *testing.T) {
	store, ctx := pgStore(t)
	_, app, _ := seedLiveDeploy(t, store, ctx, "history-coverage", "history-coverage")
	pool := *store.Pool()
	now := time.Now().UTC()
	if _, err := pool.Exec(ctx, `INSERT INTO request_telemetry_coverage(node_name,boot_id,sequence,enabled,sampling_basis_points,dropped_total,pending_count,source_at,received_at,healthy_since)
 SELECT name,gen_random_uuid(),1,true,10000,0,0,$1,$1,$1::timestamptz-interval '4 hours' FROM compute_nodes WHERE active
 ON CONFLICT(node_name) DO UPDATE SET enabled=true,sampling_basis_points=10000,source_at=$1,received_at=$1,healthy_since=$1::timestamptz-interval '4 hours'`, now); err != nil {
		t.Fatal(err)
	}
	var node string
	if err := pool.QueryRow(ctx, "SELECT name FROM compute_nodes WHERE active LIMIT 1").Scan(&node); err != nil {
		t.Fatal(err)
	}
	from, until := now.Add(-3*time.Hour), now.Add(-2*time.Hour)
	if _, err := pool.Exec(ctx, "INSERT INTO request_analytics_rollup_audit(app_id,audited_since) VALUES($1,$2)", app, from.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	assertComplete := func() {
		t.Helper()
		gaps, err := store.RequestTelemetryCompleteness(ctx, app, from, until)
		if err != nil || len(gaps) != 0 {
			t.Fatalf("historical coverage: %v %v", gaps, err)
		}
	}
	assertComplete()
	if _, err := pool.Exec(ctx, `INSERT INTO request_telemetry_app_gaps(node_name,app_id,last_gap_at,pending_count,dropped_total,updated_at) VALUES($1,$2,$3,0,1,$4)`, node, app, now.Add(-30*time.Minute), now); err != nil {
		t.Fatal(err)
	}
	assertComplete()
	if _, err := pool.Exec(ctx, "UPDATE request_telemetry_coverage SET enabled=false,healthy_since=NULL,received_at=$1", now); err != nil {
		t.Fatal(err)
	}
	assertComplete()
	// Keep each real loss even after the latest app-gap row advances again.
	if _, err := pool.Exec(ctx, "UPDATE request_telemetry_app_gaps SET last_gap_at=$3,updated_at=$4 WHERE node_name=$1 AND app_id=$2", node, app, from.Add(time.Minute), from.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "UPDATE request_telemetry_app_gaps SET last_gap_at=$3,updated_at=$3 WHERE node_name=$1 AND app_id=$2", node, app, now); err != nil {
		t.Fatal(err)
	}
	gaps, err := store.RequestTelemetryCompleteness(ctx, app, from, until)
	if err != nil || !slices.Contains(gaps, "telemetry_window_incomplete") {
		t.Fatalf("lost historical gap: %v %v", gaps, err)
	}
}

func TestAnalyticsDeploymentRollupsUseUncappedPopulation(t *testing.T) {
	store, ctx := pgStore(t)
	account, app, dep := seedLiveDeploy(t, store, ctx, "deployment-rollups", "deployment-rollups")
	at := time.Now().UTC().Truncate(time.Minute).Add(-10 * time.Minute)
	row := state.RequestAnalyticsRollup{AppID: uuid.MustParse(app), AccountID: uuid.MustParse(account), DeploymentID: uuid.MustParse(dep), BucketStart: at, DimensionKind: "route", Dimension: "GET /", Method: "GET", Status: 200, Requests: 100}
	row.LatencyHist[0] = 100
	if err := store.AddRequestAnalyticsRollups(ctx, []state.RequestAnalyticsRollup{row}, nil); err != nil {
		t.Fatal(err)
	}
	global, err := store.RequestAnalyticsRollupByDeployment(ctx, sqlc.RequestTelemetryAnalyticsByDeploymentParams{AppID: mustPgUUID(t, app), AccountID: mustPgUUID(t, account), ReceivedAt: ts(at), ReceivedAt_2: ts(at.Add(time.Minute)), Limit: 5})
	if err != nil || len(global) != 1 || global[0].Requests != 100 || global[0].TotalRequests != 100 {
		t.Fatalf("deployment totals: %+v %v", global, err)
	}
	routes, err := store.RequestAnalyticsRollupByRouteDeployment(ctx, sqlc.RequestTelemetryAnalyticsByRouteDeploymentParams{AppID: mustPgUUID(t, app), AccountID: mustPgUUID(t, account), ReceivedAt: ts(at), ReceivedAt_2: ts(at.Add(time.Minute)), RouteLimit: 50, DeploymentLimit: 5})
	if err != nil || len(routes) != 1 || routes[0].Requests != 100 || routes[0].DeploymentID != dep {
		t.Fatalf("route/deployment totals: %+v %v", routes, err)
	}
}

func TestAnalyticsCountingHistoryIgnoresLaterRawLoss(t *testing.T) {
	store, ctx := pgStore(t)
	_, app, _ := seedLiveDeploy(t, store, ctx, "counting-history", "counting-history")
	pool := *store.Pool()
	now := time.Now().UTC()
	from, until := now.Add(-3*time.Hour), now.Add(-2*time.Hour)
	if _, err := pool.Exec(ctx, `INSERT INTO request_telemetry_coverage(node_name,boot_id,sequence,enabled,sampling_basis_points,dropped_total,pending_count,source_at,received_at,healthy_since)
 SELECT name,gen_random_uuid(),1,true,10000,0,0,$1,$1,$1::timestamptz-interval '4 hours' FROM compute_nodes WHERE active`, now); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO request_analytics_node_coverage(node_name,boot_id,counting_since,heartbeat_at,unattributed_dropped_total)
 SELECT node_name,boot_id,$1::timestamptz-interval '4 hours',$1,0 FROM request_telemetry_coverage`, now); err != nil {
		t.Fatal(err)
	}
	var node string
	if err := pool.QueryRow(ctx, `SELECT node_name FROM request_telemetry_coverage LIMIT 1`).Scan(&node); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO request_telemetry_app_gaps(node_name,app_id,last_gap_at,pending_count,dropped_total,updated_at) VALUES($1,$2,$3,0,1,$4)`, node, app, from.Add(time.Minute), from.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	assertComplete := func() {
		t.Helper()
		gaps, err := store.RequestAnalyticsCompleteness(ctx, app, from, until)
		if err != nil || len(gaps) != 0 {
			t.Fatalf("gateway counts inherited raw loss or later state: %v %v", gaps, err)
		}
	}
	assertComplete()
	if gaps, err := store.RequestTelemetryCompleteness(ctx, app, from, until); err != nil || !slices.Contains(gaps, "telemetry_window_incomplete") {
		t.Fatalf("raw response ignored its loss: %v %v", gaps, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE request_analytics_node_coverage SET counting_since=NULL`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO request_analytics_app_gaps(node_name,app_id,last_gap_at,pending_count,updated_at) VALUES($1,$2,$3,0,$4)`, node, app, now.Add(-30*time.Minute), now); err != nil {
		t.Fatal(err)
	}
	assertComplete()
	if _, err := pool.Exec(ctx, `UPDATE request_analytics_app_gaps SET last_gap_at=$3,updated_at=$4 WHERE node_name=$1 AND app_id=$2`, node, app, from.Add(time.Minute), from.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if gaps, err := store.RequestAnalyticsCompleteness(ctx, app, from, until); err != nil || !slices.Contains(gaps, "telemetry_window_incomplete") {
		t.Fatalf("counting loss was hidden: %v %v", gaps, err)
	}
}

func TestAnalyticsHistoryRequiresProofForRetiredSilentNodes(t *testing.T) {
	store, ctx := pgStore(t)
	_, app, _ := seedLiveDeploy(t, store, ctx, "retired-history", "retired-history")
	pool := *store.Pool()
	now := time.Now().UTC()
	from, until := now.Add(-3*time.Hour), now.Add(-2*time.Hour)
	if _, err := pool.Exec(ctx, `INSERT INTO request_telemetry_coverage(node_name,boot_id,sequence,enabled,sampling_basis_points,dropped_total,pending_count,source_at,received_at,healthy_since)
 SELECT name,gen_random_uuid(),1,true,10000,0,0,$1,$1,$1::timestamptz-interval '4 hours' FROM compute_nodes WHERE active`, now); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO request_analytics_rollup_audit(app_id,audited_since) VALUES($1,$2)`, app, from.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO request_analytics_coverage_watermarks(node_name,registered_at,previous_source_at,source_at) VALUES('retired-silent',$1,$2,$2)`, from.Add(-time.Hour), from.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	gaps, err := store.RequestAnalyticsCompleteness(ctx, app, from, until)
	if err != nil || !slices.Contains(gaps, "telemetry_coverage_unknown") {
		t.Fatalf("silence before retirement was treated as proven absence: %v %v", gaps, err)
	}
}
