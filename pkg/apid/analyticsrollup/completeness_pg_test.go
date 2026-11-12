//go:build !no_pg

// adr: 20261103113952

package analyticsrollup_test

import (
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

// A node counting analytics itself is judged by its analytics gaps when
// rollups answer: a raw row it lost leaves the window complete, an
// analytics loss does not, and a heartbeat without the analytics part
// falls back to raw delivery (amendment 6). Raw completeness, which route
// removal reads, still sees the raw loss.
func TestAnalyticsCompletenessJudgesCountingNodesByAnalyticsGaps(t *testing.T) {
	ctx := t.Context()
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	store := state.NewPgStore(pool)
	account, err := store.CreateAccount(ctx, "analytics-completeness@example.test", api.PlanScale)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "analytics-completeness"})
	if err != nil {
		t.Fatal(err)
	}
	rows, err := pool.Query(ctx, `SELECT name FROM compute_nodes WHERE active ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	var nodes []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		nodes = append(nodes, n)
	}
	rows.Close()
	if len(nodes) == 0 {
		t.Fatal("no active compute node in the migrated database")
	}
	boot := uuid.NewString()
	seq := int64(0)
	var drops int64
	// heartbeat reports every node; the first carries gap, and analytics
	// when set.
	heartbeat := func(gap state.TelemetryAppGap, analytics *state.AnalyticsCoverage) {
		t.Helper()
		seq++
		drops += gap.DroppedCount
		for i, node := range nodes {
			c := state.TelemetryCoverage{NodeName: node, BootID: boot, Sequence: seq, Enabled: true, SamplingBasisPoints: 10000,
				SourceAt: time.Now().UTC(), AppScoped: true, Analytics: analytics}
			if i == 0 {
				c.DroppedTotal = drops
				if gap.AppID != "" {
					c.AppGaps = []state.TelemetryAppGap{gap}
				}
			}
			if err := store.RecordTelemetryCoverage(ctx, c); err != nil {
				t.Fatalf("heartbeat %d %s: %v", seq, node, err)
			}
		}
	}
	counting := &state.AnalyticsCoverage{Counting: true}
	heartbeat(state.TelemetryAppGap{}, counting)
	time.Sleep(20 * time.Millisecond)
	from := time.Now().UTC()
	until := from.Add(time.Millisecond)
	time.Sleep(20 * time.Millisecond)

	// The gateway lost a raw row it had counted.
	heartbeat(state.TelemetryAppGap{AppID: app.ID, DroppedCount: 1}, counting)
	analytics, err := store.RequestAnalyticsCompleteness(ctx, app.ID, from, until)
	if err != nil || len(analytics) != 0 {
		t.Fatalf("analytics blockers = %v, %v; want none for a raw loss the gateway counted", analytics, err)
	}
	raw, err := store.RequestTelemetryCompleteness(ctx, app.ID, from, until)
	if err != nil || !slices.Contains(raw, "telemetry_window_incomplete") {
		t.Fatalf("raw blockers = %v, %v; want the raw loss", raw, err)
	}

	// Late rollups are pending, then an analytics loss marks the window.
	heartbeat(state.TelemetryAppGap{AppID: app.ID, AnalyticsPendingCount: 2}, counting)
	if got, err := store.RequestAnalyticsCompleteness(ctx, app.ID, from, until); err != nil || !slices.Contains(got, "telemetry_ingestion_pending") {
		t.Fatalf("blockers with late rollups = %v, %v", got, err)
	}
	heartbeat(state.TelemetryAppGap{AppID: app.ID, AnalyticsDroppedCount: 1}, counting)
	if got, err := store.RequestAnalyticsCompleteness(ctx, app.ID, from, until); err != nil || !slices.Contains(got, "telemetry_window_incomplete") || slices.Contains(got, "telemetry_ingestion_pending") {
		t.Fatalf("blockers after an analytics loss = %v, %v; want incomplete, nothing pending", got, err)
	}

	// A window starting after the loss is whole again.
	time.Sleep(20 * time.Millisecond)
	later := time.Now().UTC()
	time.Sleep(20 * time.Millisecond)
	heartbeat(state.TelemetryAppGap{}, counting)
	if got, err := store.RequestAnalyticsCompleteness(ctx, app.ID, later, later.Add(time.Millisecond)); err != nil || len(got) != 0 {
		t.Fatalf("blockers after the loss = %v, %v; want none", got, err)
	}

	// A heartbeat without the analytics part: the node is judged by raw
	// delivery, whose loss came after the first window began.
	heartbeat(state.TelemetryAppGap{}, nil)
	if got, err := store.RequestAnalyticsCompleteness(ctx, app.ID, from, until); err != nil || !slices.Contains(got, "telemetry_window_incomplete") {
		t.Fatalf("blockers without the analytics part = %v, %v; want the raw loss", got, err)
	}

	// A gateway that stops counting restarts counting_since.
	heartbeat(state.TelemetryAppGap{}, &state.AnalyticsCoverage{Counting: false})
	var since *time.Time
	if err := pool.QueryRow(ctx, `SELECT counting_since FROM request_analytics_node_coverage WHERE node_name=$1`, nodes[0]).Scan(&since); err != nil || since != nil {
		t.Fatalf("counting_since = %v, %v; want NULL once counting stops", since, err)
	}
}
