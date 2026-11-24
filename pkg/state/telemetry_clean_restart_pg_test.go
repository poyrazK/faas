package state_test

import (
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/state"
)

// A gateway that delivered every row before exiting ends with a stopped
// heartbeat; its node's next boot continues the coverage. A boot after an
// unclean exit, or one that lost rows it cannot attribute, starts fresh
// (2026-11-24 telemetry audit: every gateway restart split analytics coverage).
func TestTelemetryCoverageCleanRestartPG(t *testing.T) {
	for _, tc := range []struct {
		name      string
		cleanStop bool
		stopAge   string
		drops     int64
		mature    bool
	}{
		{"clean restart keeps coverage", true, "0 seconds", 0, true},
		{"unclean restart starts fresh", false, "0 seconds", 0, false},
		{"slow clean restart starts fresh", true, "1 minute", 0, false},
		{"clean restart with unattributed loss starts fresh", true, "0 seconds", 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, pool, ctx, _, _, _, _, _ := routeRemovalFixture(t)
			var node, boot string
			if err := pool.QueryRow(ctx, `SELECT node_name,boot_id::text FROM request_telemetry_coverage LIMIT 1`).Scan(&node, &boot); err != nil {
				t.Fatal(err)
			}
			stop := state.TelemetryCoverage{NodeName: node, BootID: boot, Sequence: 2, Enabled: true, SamplingBasisPoints: 10000,
				SourceAt: time.Now().UTC(), AppScoped: true, CleanStop: tc.cleanStop}
			if err := store.RecordTelemetryCoverage(ctx, stop); err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(ctx, `UPDATE request_telemetry_coverage SET clean_stop_at=clean_stop_at-$2::interval,received_at=received_at-$2::interval WHERE node_name=$1`, node, tc.stopAge); err != nil {
				t.Fatal(err)
			}
			next := state.TelemetryCoverage{NodeName: node, BootID: uuid.NewString(), Sequence: 1, Enabled: true, SamplingBasisPoints: 10000,
				SourceAt: time.Now().UTC().Add(time.Millisecond), AppScoped: true, DroppedTotal: tc.drops, UnattributedDroppedTotal: tc.drops}
			if err := store.RecordTelemetryCoverage(ctx, next); err != nil {
				t.Fatal(err)
			}
			var mature, stopCleared bool
			if err := pool.QueryRow(ctx, `SELECT coalesce(healthy_since<clock_timestamp()-interval '1 hour',false), clean_stop_at IS NULL FROM request_telemetry_coverage WHERE node_name=$1`, node).Scan(&mature, &stopCleared); err != nil {
				t.Fatal(err)
			}
			if mature != tc.mature || !stopCleared {
				t.Fatalf("mature coverage %v (want %v), stop cleared %v", mature, tc.mature, stopCleared)
			}
		})
	}
}

// Rows in flight at heartbeat time are not lost: only a growing dropped
// total marks the app's window incomplete, and a live backlog reads pending
// for recent windows only.
func TestTelemetryCoverageGapsOnlyOnLossPG(t *testing.T) {
	store, pool, ctx, _, app, _, _, _ := routeRemovalFixture(t)
	var node, boot string
	if err := pool.QueryRow(ctx, `SELECT node_name,boot_id::text FROM request_telemetry_coverage LIMIT 1`).Scan(&node, &boot); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "INSERT INTO request_analytics_rollup_audit(app_id,audited_since) VALUES($1,clock_timestamp()-interval '3 hours')", app.ID); err != nil {
		t.Fatal(err)
	}
	seq := int64(1)
	beat := func(pending int32, dropped int64) {
		t.Helper()
		seq++
		report := state.TelemetryCoverage{NodeName: node, BootID: boot, Sequence: seq, Enabled: true, SamplingBasisPoints: 10000,
			SourceAt: time.Now().UTC(), AppScoped: true, PendingCount: pending, DroppedTotal: dropped}
		if pending > 0 || dropped > 0 {
			report.AppGaps = []state.TelemetryAppGap{{AppID: app.ID, PendingCount: pending, DroppedCount: dropped}}
		}
		if err := store.RecordTelemetryCoverage(ctx, report); err != nil {
			t.Fatal(err)
		}
	}
	gapRows := func() int {
		t.Helper()
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM request_analytics_coverage_gaps WHERE app_id=$1`, app.ID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	now := time.Now().UTC()
	beat(3, 0)
	beat(0, 0)
	if n := gapRows(); n != 0 {
		t.Fatalf("in-flight rows wrote %d coverage gaps, want none", n)
	}
	time.Sleep(20 * time.Millisecond)
	backlog := time.Now().UTC()
	time.Sleep(20 * time.Millisecond)
	beat(2, 0)
	gaps, err := store.RequestTelemetryCompleteness(ctx, app.ID, now.Add(-time.Hour), backlog)
	if err != nil || len(gaps) != 1 || gaps[0] != "telemetry_ingestion_pending" {
		t.Fatalf("window reaching into a live backlog: %v %v, want telemetry_ingestion_pending", gaps, err)
	}
	gaps, err = store.RequestTelemetryCompleteness(ctx, app.ID, now.Add(-2*time.Hour), now.Add(-time.Hour))
	if err != nil || len(gaps) != 0 {
		t.Fatalf("old window with a live backlog: %v %v, want none", gaps, err)
	}
	beat(0, 4)
	if n := gapRows(); n != 1 {
		t.Fatalf("lost rows wrote %d coverage gaps, want 1", n)
	}
}

// After an unclean gateway restart a release guard judges the covered tail of
// its window rather than holding for good.
func TestRequestTelemetryCoveredFromPG(t *testing.T) {
	store, pool, ctx, _, _, _, _, _ := routeRemovalFixture(t)
	now := time.Now().UTC()
	from, until := now.Add(-30*time.Minute).Truncate(time.Minute), now.Truncate(time.Minute)
	tail, ok, err := store.RequestTelemetryCoveredFrom(ctx, from, until)
	if err != nil || !ok || !tail.Equal(from) {
		t.Fatalf("covered window: %v %v %v, want %v", tail, ok, err, from)
	}
	restart := now.Add(-10 * time.Minute)
	if _, err := pool.Exec(ctx, `UPDATE request_telemetry_coverage SET healthy_since=$1`, restart); err != nil {
		t.Fatal(err)
	}
	want := restart.Truncate(time.Minute).Add(time.Minute)
	tail, ok, err = store.RequestTelemetryCoveredFrom(ctx, from, until)
	if err != nil || !ok || !tail.Equal(want) {
		t.Fatalf("restart inside the window: %v %v %v, want %v", tail, ok, err, want)
	}
	if _, err := pool.Exec(ctx, `UPDATE request_telemetry_coverage SET healthy_since=NULL,enabled=false`); err != nil {
		t.Fatal(err)
	}
	if _, ok, err = store.RequestTelemetryCoveredFrom(ctx, from, until); err != nil || ok {
		t.Fatalf("disabled gateway: ok %v err %v, want no covered tail", ok, err)
	}
}

// A loss marks the whole backlog it was lost from, and a long backlog holds
// every window it touches (code review of #7982).
func TestTelemetryCoverageLongBacklogPG(t *testing.T) {
	store, pool, ctx, _, app, _, _, _ := routeRemovalFixture(t)
	var node string
	if err := pool.QueryRow(ctx, `SELECT node_name FROM request_telemetry_coverage LIMIT 1`).Scan(&node); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "INSERT INTO request_analytics_rollup_audit(app_id,audited_since) VALUES($1,clock_timestamp()-interval '3 hours')", app.ID); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	// A backlog that began 30 minutes ago and is still pending.
	if _, err := pool.Exec(ctx, `INSERT INTO request_telemetry_app_gaps(node_name,app_id,last_gap_at,pending_count,pending_since,dropped_total,updated_at)
 VALUES($1,$2,clock_timestamp(),5,clock_timestamp()-interval '30 minutes',0,clock_timestamp())`, node, app.ID); err != nil {
		t.Fatal(err)
	}
	from, until := now.Add(-25*time.Minute), now.Add(-20*time.Minute)
	gaps, err := store.RequestTelemetryCompleteness(ctx, app.ID, from, until)
	if err != nil || len(gaps) != 1 || gaps[0] != "telemetry_ingestion_pending" {
		t.Fatalf("window inside a 30-minute backlog: %v %v, want telemetry_ingestion_pending", gaps, err)
	}
	// The gateway gives the backlog up: the gap reaches back to its start.
	if _, err := pool.Exec(ctx, `UPDATE request_telemetry_app_gaps SET pending_count=0,pending_since=NULL,dropped_total=5,last_gap_at=clock_timestamp(),updated_at=clock_timestamp() WHERE node_name=$1 AND app_id=$2`, node, app.ID); err != nil {
		t.Fatal(err)
	}
	gaps, err = store.RequestTelemetryCompleteness(ctx, app.ID, from, until)
	if err != nil || len(gaps) != 1 || gaps[0] != "telemetry_window_incomplete" {
		t.Fatalf("window inside a lost backlog: %v %v, want telemetry_window_incomplete", gaps, err)
	}
}

// A stale pending rollup marker settles only when its own minute rolled up:
// another minute's hour row proves nothing (code review of #7982).
func TestRequestAnalyticsStaleMarkerNeedsOwnMinutePG(t *testing.T) {
	store, pool, ctx, account, app, baseline, _, _ := routeRemovalFixture(t)
	now := time.Now().UTC()
	hour := now.Add(-2 * time.Hour).Truncate(time.Hour)
	failed := hour.Add(5 * time.Minute)
	if _, err := pool.Exec(ctx, "INSERT INTO request_analytics_rollup_audit(app_id,audited_since) VALUES($1,$2)", app.ID, hour.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	rollup := func(table string, bucket time.Time) {
		t.Helper()
		if _, err := pool.Exec(ctx, `INSERT INTO `+table+`(app_id,bucket_start,dimension_kind,dimension,method,status,deployment_id,account_id,requests,error_requests,cold_boots,request_time_ms,latency_max_ms,latency_hist)
 VALUES($1,$2,'route','GET /','GET',200,$3,$4,1,0,0,1,1,array_fill(0::bigint,ARRAY[45]))`, app.ID, bucket, baseline.ID, account.ID); err != nil {
			t.Fatal(err)
		}
	}
	rollup("request_analytics_hours", hour)
	rollup("request_analytics_minutes", hour)
	if _, err := pool.Exec(ctx, `INSERT INTO request_analytics_rollup_windows(id,app_id,bucket_start,state) VALUES(gen_random_uuid(),$1,$2,'pending')`, app.ID, failed); err != nil {
		t.Fatal(err)
	}
	gaps, err := store.RequestAnalyticsCompleteness(ctx, app.ID, hour, hour.Add(time.Hour))
	if err != nil || !slices.Contains(gaps, "telemetry_window_incomplete") {
		t.Fatalf("minute never rolled up: %v %v, want telemetry_window_incomplete", gaps, err)
	}
	rollup("request_analytics_minutes", failed)
	gaps, err = store.RequestAnalyticsCompleteness(ctx, app.ID, hour, hour.Add(time.Hour))
	if err != nil || slices.Contains(gaps, "telemetry_window_incomplete") || slices.Contains(gaps, "telemetry_ingestion_pending") {
		t.Fatalf("minute rolled up: %v %v, want the marker settled", gaps, err)
	}
}

// A node that finished draining out of the fleet before a window began
// served none of it; its last watermark no longer leaves every window
// telemetry_coverage_unknown (prod 2026-11-24, retired fsn-2/fsn-3).
func TestRequestTelemetryCompletenessIgnoresDrainedNodesPG(t *testing.T) {
	store, pool, ctx, _, app, _, _, _ := routeRemovalFixture(t)
	now := time.Now().UTC()
	from, until := now.Add(-time.Hour), now.Add(-2*time.Minute)
	if _, err := pool.Exec(ctx, "INSERT INTO request_analytics_rollup_audit(app_id,audited_since) VALUES($1,$2)", app.ID, from.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO compute_nodes(name,target_url,vpcpus,mem_mb,max_concurrency,admission_ceiling_mb,created_at) VALUES('drained-node','unix:///run/test.sock',2,1024,10,512,$1)`, now.Add(-72*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO request_analytics_coverage_watermarks(node_name,registered_at,previous_source_at,source_at) VALUES('drained-node',$1,$2,$2)`, now.Add(-72*time.Hour), now.Add(-48*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE compute_nodes SET lifecycle='retired',drain_initiated_at=$1,drain_completed_at=$1 WHERE name='drained-node'`, now.Add(-24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	gaps, err := store.RequestTelemetryCompleteness(ctx, app.ID, from, until)
	if err != nil || len(gaps) != 0 {
		t.Fatalf("drained node: gaps %v err %v, want none", gaps, err)
	}
	// Draining that finished inside the window may still have served it.
	if _, err := pool.Exec(ctx, `UPDATE compute_nodes SET drain_initiated_at=$1,drain_completed_at=$1 WHERE name='drained-node'`, now.Add(-30*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if gaps, err = store.RequestTelemetryCompleteness(ctx, app.ID, from, until); err != nil || len(gaps) == 0 {
		t.Fatalf("node drained inside the window: gaps %v err %v, want coverage unknown", gaps, err)
	}
}
