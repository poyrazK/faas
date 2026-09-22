// spec: §6.2 — queue_lag and queue_depth are different quantities.
package targets

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// lagPolicy declares a queue_lag target and nothing else.
func lagPolicy(value float64) *state.ScalingPolicy {
	return &state.ScalingPolicy{
		Targets: []state.ScalingTarget{{Metric: api.ScalingMetricQueueLag, Value: value}},
	}
}

// TestQueueLag_DoesNotFallBackToQueueDepth is the first half of the
// conflation.
//
// queue_lag measures what is still sitting on the BROKER. queue_depth
// measures what the poller has already pulled into Gregale's own queue — at
// most one batch per tick. When no broker answers, a metric named "lag" must
// report NO SIGNAL rather than confidently reporting the other quantity.
//
// Here the local queue holds 900 items and no broker reader is wired. A
// queue_lag target of 10 would demand 90 instances if depth were substituted.
func TestQueueLag_DoesNotFallBackToQueueDepth(t *testing.T) {
	store := &fakeStore{apps: []state.App{{
		ID:             "app1",
		MaxConcurrency: 100,
		ScalingPolicy:  lagPolicy(10),
	}}}
	engine := &fakeEngine{}
	queue := &fakeQueueStats{byApp: map[string]state.QueueStats{"app1": {Depth: 900}}}

	tr := New(store, nil, engine, &fakeLedger{conc: map[string]int{"app1": 1}}, Options{
		Metrics:          wire.NewOpsMetrics("schedd"),
		QueueStatsReader: queue,
		// No BrokerLagReader.
	})
	if err := tr.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if len(engine.admitCalls) != 0 {
		t.Fatalf("admitted %v on a queue_lag target with no broker reading; "+
			"the local queue depth was substituted for broker lag", engine.admitCalls)
	}
}

// TestQueueDepth_IsNotOverwrittenByBrokerLag is the other half.
//
// A broker reading must not be written into queue.Depth, or an app scaling on
// queue_depth silently scales on lag instead. Here the broker reports a huge
// lag while the local queue is nearly empty; a queue_depth target must see
// the queue.
func TestQueueDepth_IsNotOverwrittenByBrokerLag(t *testing.T) {
	store := &fakeStore{apps: []state.App{{
		ID:             "app1",
		MaxConcurrency: 100,
		ScalingPolicy: &state.ScalingPolicy{
			Targets: []state.ScalingTarget{{Metric: api.ScalingMetricQueueDepth, Value: 10}},
		},
	}}}
	engine := &fakeEngine{}
	queue := &fakeQueueStats{byApp: map[string]state.QueueStats{"app1": {Depth: 2}}}
	broker := &fakeBrokerLag{byApp: map[string]int64{"app1": 50000}}

	tr := New(store, nil, engine, &fakeLedger{conc: map[string]int{"app1": 1}}, Options{
		Metrics:          wire.NewOpsMetrics("schedd"),
		QueueStatsReader: queue,
		BrokerLagReader:  broker,
	})
	if err := tr.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	// Depth 2 across 1 instance against a target of 10 is not hot.
	if len(engine.admitCalls) != 0 {
		t.Fatalf("admitted %v on a queue_depth target; the broker's lag of 50000 "+
			"was written into queue.Depth and scaled the app on the wrong quantity",
			engine.admitCalls)
	}
}

// TestQueueLag_ScalesOnBrokerReading is the positive control for the pair
// above: with a broker reading present, queue_lag does scale.
func TestQueueLag_ScalesOnBrokerReading(t *testing.T) {
	store := &fakeStore{apps: []state.App{{
		ID:             "app1",
		MaxConcurrency: 100,
		ScalingPolicy:  lagPolicy(100),
	}}}
	engine := &burstFakeEngine{fakeEngine: &fakeEngine{}}
	broker := &fakeBrokerLag{byApp: map[string]int64{"app1": 400}}

	tr := New(store, nil, engine, &fakeLedger{conc: map[string]int{"app1": 2}}, Options{
		Metrics:         wire.NewOpsMetrics("schedd"),
		BrokerLagReader: broker,
	})
	if err := tr.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	// ceil(400/100) = 4 desired, minus 2 live = 2 admissions.
	if len(engine.burstCounts) != 1 || engine.burstCounts[0] != 2 {
		t.Fatalf("burstCounts = %v, want [2]", engine.burstCounts)
	}
}

// TestBrokerLag_PreservesBindingBreakdown pins the collateral damage of the
// old early return. Short-circuiting on the broker reading discarded the
// per-binding queue rows entirely, so a worker app with bindings lost its
// per-binding gauges and its binding-aware worker sizing the moment a broker
// answered.
func TestBrokerLag_PreservesBindingBreakdown(t *testing.T) {
	app := state.App{
		ID:             "app1",
		AccountID:      "acct1",
		MaxConcurrency: 100,
		ScalingPolicy: &state.ScalingPolicy{
			Targets: []state.ScalingTarget{{Metric: api.ScalingMetricQueueDepth, Value: 5}},
		},
	}
	bindings := &fakeQueueBindings{
		byApp: map[string][]state.QueueBinding{
			"app1": {{QueueName: "orders", Enabled: true}, {QueueName: "events", Enabled: true}},
		},
		byQueue: map[string]state.QueueStats{
			"orders": {Depth: 7},
			"events": {Depth: 3},
		},
	}
	tr := New(&fakeStore{apps: []state.App{app}}, nil, &fakeEngine{},
		&fakeLedger{conc: map[string]int{"app1": 1}}, Options{
			Metrics:                 wire.NewOpsMetrics("schedd"),
			QueueBindingStatsReader: bindings,
			BrokerLagReader:         &fakeBrokerLag{byApp: map[string]int64{"app1": 999}},
		})

	sig, ok, err := tr.readQueueState(context.Background(), app, time.Now())
	if err != nil || !ok {
		t.Fatalf("readQueueState = (%v, %v)", ok, err)
	}
	if len(sig.bindings) != 2 {
		t.Errorf("bindings = %d, want 2: a broker reading discarded the per-binding breakdown", len(sig.bindings))
	}
	if sig.queue.Depth != 10 {
		t.Errorf("aggregate depth = %d, want 10 (7+3): the broker reading overwrote it", sig.queue.Depth)
	}
	if !sig.haveBrokerLag || sig.brokerLag != 999 {
		t.Errorf("brokerLag = (%d, %v), want (999, true): the lag must be carried alongside the depth",
			sig.brokerLag, sig.haveBrokerLag)
	}
}
