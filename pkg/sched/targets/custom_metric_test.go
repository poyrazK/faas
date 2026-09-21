// adr: 201 — custom application metrics as a scaling signal.
package targets

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

type fakeCustomMetrics struct {
	byApp map[string][]state.CustomMetric
	err   error
}

func (f *fakeCustomMetrics) ListCustomMetrics(_ context.Context, appID string) ([]state.CustomMetric, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.byApp[appID], nil
}

func customPolicy(name string, value float64) *state.ScalingPolicy {
	return &state.ScalingPolicy{
		Targets: []state.ScalingTarget{{Metric: api.ScalingMetricCustom, Name: name, Value: value}},
	}
}

func customTrigger(t *testing.T, policy *state.ScalingPolicy, reader CustomMetricReader, conc int) (*Trigger, *burstFakeEngine) {
	t.Helper()
	store := &fakeStore{apps: []state.App{{
		ID: "app1", MaxConcurrency: 50, ScalingPolicy: policy,
	}}}
	engine := &burstFakeEngine{fakeEngine: &fakeEngine{}}
	return New(store, nil, engine, &fakeLedger{conc: map[string]int{"app1": conc}}, Options{
		Metrics:            wire.NewOpsMetrics("schedd"),
		CustomMetricReader: reader,
	}), engine
}

// TestCustomMetric_ScalesOnPushedValue is the contract: a fresh pushed gauge
// drives capacity as a fleet-total backlog, desired = ceil(value / target).
func TestCustomMetric_ScalesOnPushedValue(t *testing.T) {
	now := time.Now()
	tr, engine := customTrigger(t, customPolicy("orders_pending", 100), &fakeCustomMetrics{
		byApp: map[string][]state.CustomMetric{
			"app1": {{Name: "orders_pending", Value: 420, ObservedAt: now}},
		},
	}, 2)
	if err := tr.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if len(engine.burstCounts) != 1 {
		t.Fatalf("burstCounts = %v, want one batch: a declared custom target did not scale", engine.burstCounts)
	}
	// ceil(420/100) = 5 desired, minus 2 live = 3 admissions.
	if engine.burstCounts[0] != 3 {
		t.Errorf("admissions = %d, want 3 (ceil(420/100) - 2)", engine.burstCounts[0])
	}
}

// TestCustomMetric_StaleValueIsNoSignal is the reason observed_at is stored.
//
// If the pusher dies — the cron stops, the customer's infrastructure has an
// outage — the last value is frozen. Treating a frozen backlog as current
// would pin the fleet at whatever it was when the pusher stopped,
// indefinitely, and bill for it.
func TestCustomMetric_StaleValueIsNoSignal(t *testing.T) {
	stale := time.Now().Add(-time.Duration(api.CustomMetricFreshnessSeconds+60) * time.Second)
	tr, engine := customTrigger(t, customPolicy("orders_pending", 10), &fakeCustomMetrics{
		byApp: map[string][]state.CustomMetric{
			"app1": {{Name: "orders_pending", Value: 99999, ObservedAt: stale}},
		},
	}, 2)
	if err := tr.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if len(engine.admitCalls) != 0 || len(engine.burstCounts) != 0 {
		t.Errorf("a stale metric admitted %v/%v; a dead pusher must not hold capacity",
			engine.admitCalls, engine.burstCounts)
	}
}

// TestCustomMetric_UnpushedNameIsNoSignal covers the declared-but-never-sent
// case. Scaling on a name with no row would mean scaling on zero, which for
// a backlog reads as "nothing to do" and is right — but it must not be a
// confident reading either, or it would suppress nothing and admit nothing
// while looking like a live signal.
func TestCustomMetric_UnpushedNameIsNoSignal(t *testing.T) {
	tr, engine := customTrigger(t, customPolicy("never_pushed", 10), &fakeCustomMetrics{
		byApp: map[string][]state.CustomMetric{
			"app1": {{Name: "a_different_metric", Value: 5000, ObservedAt: time.Now()}},
		},
	}, 1)
	if err := tr.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if len(engine.admitCalls) != 0 || len(engine.burstCounts) != 0 {
		t.Errorf("an undeclared metric's value leaked into the decision: %v/%v",
			engine.admitCalls, engine.burstCounts)
	}
}

// TestCustomMetric_ReaderErrorDoesNotAdmit pins the failure direction. A
// store error must not be read as a backlog of zero OR as a reason to scale;
// it is simply no reading.
func TestCustomMetric_ReaderErrorDoesNotAdmit(t *testing.T) {
	tr, engine := customTrigger(t, customPolicy("orders_pending", 10),
		&fakeCustomMetrics{err: errors.New("pg down")}, 1)
	if err := tr.Tick(context.Background()); err != nil {
		t.Fatalf("Tick returned an error; a transient store failure must not abort the sweep: %v", err)
	}
	if len(engine.admitCalls) != 0 || len(engine.burstCounts) != 0 {
		t.Errorf("a reader error admitted %v/%v", engine.admitCalls, engine.burstCounts)
	}
}

// TestCustomMetric_NilReaderIsNoSignal covers a schedd with no reader wired.
func TestCustomMetric_NilReaderIsNoSignal(t *testing.T) {
	tr, engine := customTrigger(t, customPolicy("orders_pending", 10), nil, 1)
	if err := tr.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if len(engine.admitCalls) != 0 || len(engine.burstCounts) != 0 {
		t.Errorf("a nil reader admitted %v/%v", engine.admitCalls, engine.burstCounts)
	}
}

// TestCustomMetric_TwoNamesArbitrate pins that an app may declare several
// custom metrics and the most demanding wins — the ADR-194 contract applied
// to a metric keyed by name rather than by metric alone.
func TestCustomMetric_TwoNamesArbitrate(t *testing.T) {
	now := time.Now()
	policy := &state.ScalingPolicy{Targets: []state.ScalingTarget{
		{Metric: api.ScalingMetricCustom, Name: "orders_pending", Value: 100},
		{Metric: api.ScalingMetricCustom, Name: "docs_queued", Value: 10},
	}}
	tr, engine := customTrigger(t, policy, &fakeCustomMetrics{
		byApp: map[string][]state.CustomMetric{"app1": {
			// orders: ceil(200/100) = 2. docs: ceil(60/10) = 6. docs wins.
			{Name: "orders_pending", Value: 200, ObservedAt: now},
			{Name: "docs_queued", Value: 60, ObservedAt: now},
		}},
	}, 2)
	if err := tr.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if len(engine.burstCounts) != 1 || engine.burstCounts[0] != 4 {
		t.Fatalf("burstCounts = %v, want [4]: the more demanding custom metric must win "+
			"(docs 6 desired beats orders 2)", engine.burstCounts)
	}
}

// TestCustomMetric_CombinesWithPlatformSignal pins that a custom metric
// arbitrates against the platform-measured ones rather than replacing them.
func TestCustomMetric_CombinesWithPlatformSignal(t *testing.T) {
	now := time.Now()
	policy := &state.ScalingPolicy{Targets: []state.ScalingTarget{
		{Metric: api.ScalingMetricConcurrentRequests, Value: 4},
		{Metric: api.ScalingMetricCustom, Name: "orders_pending", Value: 100},
	}}
	store := &fakeStore{apps: []state.App{{ID: "app1", MaxConcurrency: 50, ScalingPolicy: policy}}}
	engine := &burstFakeEngine{fakeEngine: &fakeEngine{}}
	tr := New(store, &fakeInstats{byApp: map[string]int64{"app1": 5}}, engine,
		&fakeLedger{conc: map[string]int{"app1": 2}}, Options{
			Metrics: wire.NewOpsMetrics("schedd"),
			CustomMetricReader: &fakeCustomMetrics{byApp: map[string][]state.CustomMetric{
				"app1": {{Name: "orders_pending", Value: 500, ObservedAt: now}},
			}},
		})
	if err := tr.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	// in-flight: ceil(5*2/4) = 3. custom: ceil(500/100) = 5. Custom wins.
	if len(engine.burstCounts) != 1 || engine.burstCounts[0] != 3 {
		t.Fatalf("burstCounts = %v, want [3]: the custom signal's 5 desired must beat "+
			"in-flight's 3", engine.burstCounts)
	}
}
