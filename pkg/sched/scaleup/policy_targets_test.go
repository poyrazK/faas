// adr: 194 — multi-signal scaling targets.
package scaleup

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// TestTrigger_DeclaredRPSTargetScales is the regression for the phantom
// ADR-194 found.
//
// `scaling.target: {metric: rps, value: N}` was in the closed metric set of
// three separate validators. It persisted to apps.scaling_policy, round-
// tripped through the API and rendered in `deploy diff`. This trigger — the
// only consumer of an RPS signal — read `apps.autoscale_target_rps` and
// never looked at the policy, so an app that configured RPS scaling through
// the documented field never scaled at all.
//
// The app here has autoscale_target_rps = 0, so a pass only happens if the
// declared target is what drove it.
func TestTrigger_DeclaredRPSTargetScales(t *testing.T) {
	store := &fakeStore{apps: []state.App{{
		ID:             "app1",
		MaxConcurrency: 5,
		// Deliberately zero: the legacy column must NOT be what makes
		// this test pass.
		AutoscaleTargetRPS: 0,
		ScalingPolicy: &state.ScalingPolicy{
			Targets: []state.ScalingTarget{{Metric: api.ScalingMetricRPS, Value: 50}},
		},
	}}}
	ledger := &fakeLedger{conc: map[string]int{"app1": 2}}
	engine := &fakeEngine{}
	tr := New(store, nil, &fakeScraper{byApp: map[string]int64{"app1": 700}}, engine, ledger,
		Options{Metrics: wire.NewOpsMetrics("test")})
	tr.ring.Touch(t0(), map[string]int64{"app1": 0})

	if err := tr.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if len(engine.admitCalls) == 0 {
		t.Fatal("no admission: a declared rps target did not reach the trigger, " +
			"which is the exact phantom ADR-194 closes")
	}
}

// TestTrigger_DeclaredCPUTargetScales covers the axis that had no declarable
// surface at all before ADR-194: autoscale_target_cpu_pct was reachable only
// through the raw column, and no manifest field wrote it.
func TestTrigger_DeclaredCPUTargetScales(t *testing.T) {
	store := &fakeStore{apps: []state.App{{
		ID:                    "app1",
		MaxConcurrency:        5,
		AutoscaleTargetCPUPct: 0,
		ScalingPolicy: &state.ScalingPolicy{
			Targets: []state.ScalingTarget{{Metric: api.ScalingMetricCPU, Value: 70}},
		},
	}}}
	ledger := &fakeLedger{conc: map[string]int{"app1": 2}}
	engine := &fakeEngine{}
	instats := &fakeInstats{byCPU: map[string]float64{"app1": 91}}
	tr := New(store, instats, &fakeScraper{}, engine, ledger,
		Options{Metrics: wire.NewOpsMetrics("test")})

	if err := tr.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if len(engine.admitCalls) == 0 {
		t.Fatal("no admission: a declared cpu target did not reach the trigger")
	}
}

// TestTrigger_DeclaredTargetOverridesLegacyColumn pins the precedence in
// ADR-194: a declared target wins over the legacy column for its own axis.
// The column says 500 RPS (nowhere near hot at 70/instance); the declared
// target says 50 (hot). The declared value must be the one that decides.
func TestTrigger_DeclaredTargetOverridesLegacyColumn(t *testing.T) {
	store := &fakeStore{apps: []state.App{{
		ID:                 "app1",
		MaxConcurrency:     5,
		AutoscaleTargetRPS: 500,
		ScalingPolicy: &state.ScalingPolicy{
			Targets: []state.ScalingTarget{{Metric: api.ScalingMetricRPS, Value: 50}},
		},
	}}}
	ledger := &fakeLedger{conc: map[string]int{"app1": 2}}
	engine := &fakeEngine{}
	tr := New(store, nil, &fakeScraper{byApp: map[string]int64{"app1": 700}}, engine, ledger,
		Options{Metrics: wire.NewOpsMetrics("test")})
	tr.ring.Touch(t0(), map[string]int64{"app1": 0})

	if err := tr.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if len(engine.admitCalls) == 0 {
		t.Fatal("no admission: the legacy column (500) beat the declared target (50)")
	}
}

// TestTrigger_LegacyColumnStillWorksForUndeclaredAxis is the other half of
// the precedence rule. An app that declares only cpu keeps whatever
// autoscale_target_rps it already had; ADR-194 must not silently disable a
// running app's RPS scaling because it adopted a cpu target.
func TestTrigger_LegacyColumnStillWorksForUndeclaredAxis(t *testing.T) {
	store := &fakeStore{apps: []state.App{{
		ID:                 "app1",
		MaxConcurrency:     5,
		AutoscaleTargetRPS: 50,
		ScalingPolicy: &state.ScalingPolicy{
			Targets: []state.ScalingTarget{{Metric: api.ScalingMetricCPU, Value: 99}},
		},
	}}}
	ledger := &fakeLedger{conc: map[string]int{"app1": 2}}
	engine := &fakeEngine{}
	// CPU is cold (below the 99 target), so only the surviving legacy RPS
	// column can produce an admission here.
	instats := &fakeInstats{byCPU: map[string]float64{"app1": 10}}
	tr := New(store, instats, &fakeScraper{byApp: map[string]int64{"app1": 700}}, engine, ledger,
		Options{Metrics: wire.NewOpsMetrics("test")})
	tr.ring.Touch(t0(), map[string]int64{"app1": 0})

	if err := tr.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if len(engine.admitCalls) == 0 {
		t.Fatal("no admission: declaring a cpu target disabled the app's existing autoscale_target_rps")
	}
}
