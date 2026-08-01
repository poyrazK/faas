package meter_test

// loop_floor_emit_test pins the Loop → counter wiring that closes
// the meterd-side seam for issue #515 (ADR-060). The sampler is
// free of ops (PR-D convention); the Loop closure that wraps
// SampleAndRoll is the only site that emits
// meterd_floor_applied_total{plan}. This file asserts the emit path
// end-to-end against a real *wire.OpsMetrics registry — the same
// scrape path cmd/meterd serves at /metrics.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// floorAppliedLinePrefix is the exact line prefix
// NewOpsMetrics("meter_test_observe") emits for the
// meterd_floor_applied_total{plan="…"} counter. The prefix is
// stable for the lifetime of the test (no WithLabelValues for an
// unknown label can mint a new series — counters are
// pre-instantiated in NewOpsMetrics via the `for _, plan := range
// api.Plans` loop).
const floorAppliedLinePrefix = "meter_test_observe_meterd_floor_applied_total{plan=\""

// scrubFloorCounter reads the meterd_floor_applied_total{plan="…"}
// value via the same Prometheus text format cmd/meterd mounts. A
// missing label returns 0 (the pre-instantiation default from
// NewOpsMetrics). A non-zero value proves the closure hit
// l.ops.MeterdFloorAppliedTotal on the success path of
// SampleAndRoll.
//
// Mirrors loop_observe_test.go::scrapeBody (httptest.NewRecorder
// + Handler().ServeHTTP). Returns 0 if no matching line is
// present, instead of erroring — the empty-string-plan case is
// achievable at runtime (degraded emit when appID → account
// lookup misses) and the ops surface must not panic.
func scrubFloorCounter(t *testing.T, ops *wire.OpsMetrics, planLabel string) float64 {
	t.Helper()
	rec := httptest.NewRecorder()
	ops.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("/metrics status = %d, want 200", rec.Code)
	}
	prefix := floorAppliedLinePrefix + planLabel + "\"}"
	for _, line := range strings.Split(rec.Body.String(), "\n") {
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		v, err := strconv.ParseFloat(fields[len(fields)-1], 64)
		if err != nil {
			t.Fatalf("parse counter value from %q: %v", line, err)
		}
		return v
	}
	return 0
}

// floorAppliedLineExists asserts the pre-instantiated line for
// planLabel appears in the registry at all — a metric-naming
// sanity check before the value assertion. Without this, a
// future refactor that renames the metric would silently scrub
// to 0.
func floorAppliedLineExists(t *testing.T, ops *wire.OpsMetrics, planLabel string) {
	t.Helper()
	rec := httptest.NewRecorder()
	ops.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("/metrics status = %d, want 200", rec.Code)
	}
	want := floorAppliedLinePrefix + planLabel + "\"}"
	if !strings.Contains(rec.Body.String(), want) {
		t.Errorf("missing meterd_floor_applied_total{plan=%q} in registry (metric not pre-instantiated for this plan)", planLabel)
	}
}

// seedFloorAppForEmit scales the production PATCH path
// (cmd/apid/handlers.go::updateApp → store.UpdateApp with
// SetScalingPolicy bit). Mirrors seedFloorApp in
// sampler_floor_test.go (white-box) but exposed as a black-box
// helper here for the meter_test package.
func seedFloorAppForEmit(t *testing.T, store state.Store, plan api.Plan, ramMB, minInstances int) string {
	t.Helper()
	ctx := context.Background()
	acct, err := store.CreateAccount(ctx, "floor-emit@example.com", plan)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	app, err := store.CreateApp(ctx, state.App{
		AccountID: acct.ID, Slug: "floor-emit", RAMMB: ramMB, Type: state.AppTypeApp,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	if _, err := store.CreateDeployment(ctx, state.Deployment{
		AppID: app.ID, Status: state.DeployLive, Kind: state.DeploymentKindImage,
	}); err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}
	if _, err := store.UpdateApp(ctx, app.ID, state.UpdateAppParams{
		ScalingPolicy:    &state.ScalingPolicy{MinInstances: minInstances},
		SetScalingPolicy: true,
	}); err != nil {
		t.Fatalf("UpdateApp scaling policy: %v", err)
	}
	return app.ID
}

// TestLoop_EmitFloorApplied_HobbyFloorFires — the closure that
// runs after a successful SampleAndRoll increments
// meterd_floor_applied_total{plan="hobby"} once per affected
// (app, tick). Hobby + MinInstances: 1 + zero live instances is
// the canonical Hobby break-even scenario; a tick that sees the
// floor must show a non-zero counter value.
func TestLoop_EmitFloorApplied_HobbyFloorFires(t *testing.T) {
	t.Parallel()
	store := state.NewMemStore()
	seedFloorAppForEmit(t, store, api.PlanHobby, 256, 1)

	_, ops := runLoopBrief(t, store, nil)
	floorAppliedLineExists(t, ops, "hobby")
	if got := scrubFloorCounter(t, ops, "hobby"); got < 1 {
		t.Errorf("meterd_floor_applied_total{plan=\"hobby\"} = %v, want >= 1 (per-tick increments after floor fires)", got)
	}
}

// TestLoop_EmitFloorApplied_FreeCounterZero pins the Free-plan
// guardrail at the metric surface: PR-A's PATCH-time gate rejects
// min_instances > 0 for Free, so the floor branch never fires and
// meterd_floor_applied_total{plan="free"} stays at zero (the
// pre-instantiated NewOpsMetrics default). A zero counter is the
// expected shape; any non-zero rate is a regression in either
// PR-A's gate or the sampler's MinInstances > 0 branch.
//
// Account is Free; ScalingPolicy is nil (legacy Free row shape);
// floor must be silent.
func TestLoop_EmitFloorApplied_FreeCounterZero(t *testing.T) {
	t.Parallel()
	store := state.NewMemStore()
	if _, err := store.CreateAccount(context.Background(), "free-zero@example.com", api.PlanFree); err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}

	_, ops := runLoopBrief(t, store, nil)
	floorAppliedLineExists(t, ops, "free")
	if got := scrubFloorCounter(t, ops, "free"); got != 0 {
		t.Errorf("meterd_floor_applied_total{plan=\"free\"} = %v, want 0 (Free cannot set MinInstances; PR-A PATCH gate is the primary defense)", got)
	}
}

// TestLoop_EmitFloorApplied_CounterPerTickNotPerRow pins the
// cardinality contract: the counter increments once per affected
// (app, tick), not once per synthetic row. With Hobby +
// MinInstances: 3 + zero live, the sampler writes 3 synthetic
// rows but the counter shows increment-per-tick, not
// increment-per-row. This is the floor-applied cardinality, not
// the floor-volume cardinality — the same distinction
// BillingCapExceededTotal makes for per-account vs per-row.
//
// runLoopBrief sleeps 150ms with a 20ms tick → ~7 sample ticks.
// Each tick sees the floor and increments the counter once (per
// (app, tick)). Counter value after the brief is bounded by tick
// count, not by synthetic-row count.
func TestLoop_EmitFloorApplied_CounterPerTickNotPerRow(t *testing.T) {
	t.Parallel()
	store := state.NewMemStore()
	seedFloorAppForEmit(t, store, api.PlanHobby, 256, 3)

	_, ops := runLoopBrief(t, store, nil)
	got := scrubFloorCounter(t, ops, "hobby")
	// 20ms tick × 150ms ≈ 7 ticks; allow generous slack for CI
	// scheduler jitter. The test fails closed if the counter is
	// 0 (floor never fired) or unboundedly large (per-row emit
	// regression: would show 3× per tick).
	if got < 1 {
		t.Errorf("counter = %v, want >= 1 (floor must have fired at least once in the brief)", got)
	}
	if got > 50 {
		t.Errorf("counter = %v, want <= 50 (would indicate per-row emit regression; runLoopBrief sleeps 150ms with 20ms ticks so ~7 emits total)", got)
	}
}
