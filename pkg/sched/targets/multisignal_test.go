// adr: 194 — multi-signal scaling targets.
package targets

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// multiPolicy declares a list of targets, the ADR-194 surface.
func multiPolicy(scaleOutCooldownS int, targets ...state.ScalingTarget) *state.ScalingPolicy {
	return &state.ScalingPolicy{
		Targets:           targets,
		ScaleOutCooldownS: scaleOutCooldownS,
	}
}

// TestTrigger_CombinesInflightAndQueueSignals is the developer-facing
// contract: declare two signals, get capacity for the more demanding one,
// and never write a rule that combines them.
//
// Both signals are kept under api.ScaleUpMaxBurstPerTick (4) on purpose: the
// per-tick burst bound clips anything larger, so a fixture with a big spread
// would pass whichever signal won and prove nothing.
//
// In-flight is 5 per instance against a target of 4 across 2 instances = 10
// total demand / 4 = 3 desired, i.e. 1 admission. The queue holds 24 against
// a per-worker budget of 5 = 5 desired, i.e. 3 admissions. The fleet must be
// provisioned for the queue's 5.
func TestTrigger_CombinesInflightAndQueueSignals(t *testing.T) {
	newTrigger := func() (*Trigger, *burstFakeEngine) {
		store := &fakeStore{apps: []state.App{{
			ID:             "app1",
			MaxConcurrency: 20,
			ScalingPolicy: multiPolicy(0,
				state.ScalingTarget{Metric: api.ScalingMetricConcurrentRequests, Value: 4},
				state.ScalingTarget{Metric: api.ScalingMetricQueueDepth, Value: 5},
			),
		}}}
		ledger := &fakeLedger{conc: map[string]int{"app1": 2}}
		instats := &fakeInstats{byApp: map[string]int64{"app1": 5}}
		queue := &fakeQueueStats{byApp: map[string]state.QueueStats{"app1": {Depth: 24}}}
		engine := &burstFakeEngine{fakeEngine: &fakeEngine{}}
		return New(store, instats, engine, ledger, Options{
			Metrics:          wire.NewOpsMetrics("schedd"),
			QueueStatsReader: queue,
		}), engine
	}

	tr, engine := newTrigger()
	if err := tr.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if len(engine.burstCounts) != 1 {
		t.Fatalf("burstCounts = %v, want exactly one admission batch", engine.burstCounts)
	}
	if engine.burstCounts[0] != 3 {
		t.Errorf("admissions = %d, want 3 (the queue signal's 5 desired, not the in-flight signal's 3)",
			engine.burstCounts[0])
	}

	// Control: with the queue axis removed, the same in-flight reading
	// yields 1. If this ever equals the combined result the assertion above
	// is vacuous — both signals would be producing the same number.
	soloStore := &fakeStore{apps: []state.App{{
		ID:             "app1",
		MaxConcurrency: 20,
		ScalingPolicy:  multiPolicy(0, state.ScalingTarget{Metric: api.ScalingMetricConcurrentRequests, Value: 4}),
	}}}
	soloEngine := &burstFakeEngine{fakeEngine: &fakeEngine{}}
	solo := New(soloStore, &fakeInstats{byApp: map[string]int64{"app1": 5}}, soloEngine,
		&fakeLedger{conc: map[string]int{"app1": 2}}, Options{Metrics: wire.NewOpsMetrics("schedd")})
	if err := solo.Tick(context.Background()); err != nil {
		t.Fatalf("solo Tick: %v", err)
	}
	if len(soloEngine.burstCounts) != 1 || soloEngine.burstCounts[0] != 1 {
		t.Fatalf("in-flight alone = %v, want [1]: the control must differ from the combined result",
			soloEngine.burstCounts)
	}
}

// TestTrigger_OneUnreadableSignalDoesNotSuppressTheOther pins the robustness
// half of the list contract. Before ADR-194 an app had exactly one target, so
// "queue reader not wired" and "do not scale this app" were the same
// statement. With a list they are not: a missing source must cost only its
// own signal.
func TestTrigger_OneUnreadableSignalDoesNotSuppressTheOther(t *testing.T) {
	store := &fakeStore{apps: []state.App{{
		ID:             "app1",
		MaxConcurrency: 10,
		ScalingPolicy: multiPolicy(0,
			state.ScalingTarget{Metric: api.ScalingMetricConcurrentRequests, Value: 2},
			state.ScalingTarget{Metric: api.ScalingMetricQueueDepth, Value: 5},
		),
	}}}
	ledger := &fakeLedger{conc: map[string]int{"app1": 2}}
	instats := &fakeInstats{byApp: map[string]int64{"app1": 8}}
	engine := &fakeEngine{}

	// No QueueStatsReader and no QueueBindings: the queue axis is declared
	// but unreadable on this schedd.
	tr := New(store, instats, engine, ledger, Options{Metrics: wire.NewOpsMetrics("schedd")})
	if err := tr.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if len(engine.admitCalls) == 0 {
		t.Fatal("no admission: an unreadable queue signal suppressed a hot in-flight signal")
	}
}

// TestTrigger_MissingQueueReaderStillNeverAdmitsBlindly is the other side of
// the same coin — an unreadable signal must not be treated as hot.
func TestTrigger_MissingQueueReaderStillNeverAdmitsBlindly(t *testing.T) {
	store := &fakeStore{apps: []state.App{{
		ID:             "app1",
		MaxConcurrency: 10,
		ScalingPolicy:  multiPolicy(0, state.ScalingTarget{Metric: api.ScalingMetricQueueDepth, Value: 5}),
	}}}
	ledger := &fakeLedger{conc: map[string]int{"app1": 2}}
	engine := &fakeEngine{}

	tr := New(store, nil, engine, ledger, Options{Metrics: wire.NewOpsMetrics("schedd")})
	if err := tr.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if len(engine.admitCalls) != 0 {
		t.Errorf("admitCalls = %v, want []: an unread backlog is not an empty one", engine.admitCalls)
	}
}

// TestTrigger_SingularTargetStillWorks pins backward compatibility. Every
// policy stored before ADR-194 uses the singular field, and the jsonb column
// was not migrated — EffectiveTargets promoting it is the only thing keeping
// those apps scaling.
func TestTrigger_SingularTargetStillWorks(t *testing.T) {
	store := &fakeStore{apps: []state.App{{
		ID:             "app1",
		MaxConcurrency: 5,
		ScalingPolicy:  scaleUpPolicy(1.0, 60),
	}}}
	ledger := &fakeLedger{conc: map[string]int{"app1": 2}}
	instats := &fakeInstats{byApp: map[string]int64{"app1": 5}}
	engine := &fakeEngine{}

	tr := New(store, instats, engine, ledger, Options{Metrics: wire.NewOpsMetrics("schedd")})
	if err := tr.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if len(engine.admitCalls) != 1 {
		t.Errorf("admitCalls = %v, want one: the pre-ADR-194 singular target must still scale", engine.admitCalls)
	}
}

// TestTrigger_MaxInstancesBoundsTheInflightAxis covers a bug ADR-194 fixed in
// passing. MaxInstances was loaded only inside the queue_depth branch, so an
// app scaling on concurrent_requests had its ScalingPolicy.MaxInstances
// silently ignored by this trigger and was bounded only by the plan cap.
func TestTrigger_MaxInstancesBoundsTheInflightAxis(t *testing.T) {
	policy := multiPolicy(0, state.ScalingTarget{Metric: api.ScalingMetricConcurrentRequests, Value: 1})
	policy.MaxInstances = 3
	store := &fakeStore{apps: []state.App{{
		ID:             "app1",
		MaxConcurrency: 20,
		ScalingPolicy:  policy,
	}}}
	ledger := &fakeLedger{conc: map[string]int{"app1": 2}}
	instats := &fakeInstats{byApp: map[string]int64{"app1": 50}}
	engine := &burstFakeEngine{fakeEngine: &fakeEngine{}}

	tr := New(store, instats, engine, ledger, Options{Metrics: wire.NewOpsMetrics("schedd")})
	if err := tr.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if len(engine.burstCounts) != 1 {
		t.Fatalf("burstCounts = %v, want one batch", engine.burstCounts)
	}
	// Unbounded demand is 50*2/1 = 100; max_instances 3 minus 2 live = 1.
	if engine.burstCounts[0] != 1 {
		t.Errorf("admissions = %d, want 1: max_instances=3 must bound the in-flight axis", engine.burstCounts[0])
	}
}

// fakeKafkaLag is a KafkaLagReader whose reading is fixed per app.
type fakeKafkaLag struct {
	byApp map[string]int64
	have  bool
}

func (f *fakeKafkaLag) LagForApp(appID string, _ time.Time) (int64, bool) {
	v, ok := f.byApp[appID]
	if !ok {
		return 0, false
	}
	return v, f.have
}

// adr: 198 — a declared kafka_lag target must actually scale the app.
//
// The signal is distinct from queue_depth and not a synonym: the Kafka poller
// pulls at most batchMax messages per tick, so queue_depth reflects what
// Gregale has already PULLED while kafka_lag reflects what is still waiting
// on the broker.
func TestTrigger_KafkaLagScales(t *testing.T) {
	store := &fakeStore{apps: []state.App{{
		ID:             "app1",
		MaxConcurrency: 20,
		ScalingPolicy:  multiPolicy(0, state.ScalingTarget{Metric: api.ScalingMetricKafkaLag, Value: 100}),
	}}}
	ledger := &fakeLedger{conc: map[string]int{"app1": 2}}
	engine := &burstFakeEngine{fakeEngine: &fakeEngine{}}
	lag := &fakeKafkaLag{byApp: map[string]int64{"app1": 450}, have: true}

	tr := New(store, nil, engine, ledger, Options{
		Metrics:        wire.NewOpsMetrics("schedd"),
		KafkaLagReader: lag,
	})
	if err := tr.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if len(engine.burstCounts) != 1 {
		t.Fatalf("burstCounts = %v, want one batch: a declared kafka_lag target did not scale", engine.burstCounts)
	}
	// ceil(450/100) = 5 desired, minus 2 live = 3 admissions.
	if engine.burstCounts[0] != 3 {
		t.Errorf("admissions = %d, want 3 (ceil(450/100) - 2)", engine.burstCounts[0])
	}
}

// adr: 198 — a stale or absent lag reading must never admit. A wedged poller
// freezes its last reading; treating that as current would pin the fleet.
func TestTrigger_KafkaLagWithoutReadingDoesNotScale(t *testing.T) {
	newTrigger := func(reader KafkaLagReader) (*Trigger, *fakeEngine) {
		store := &fakeStore{apps: []state.App{{
			ID:             "app1",
			MaxConcurrency: 20,
			ScalingPolicy:  multiPolicy(0, state.ScalingTarget{Metric: api.ScalingMetricKafkaLag, Value: 10}),
		}}}
		engine := &fakeEngine{}
		return New(store, nil, engine, &fakeLedger{conc: map[string]int{"app1": 2}},
			Options{Metrics: wire.NewOpsMetrics("schedd"), KafkaLagReader: reader}), engine
	}
	// A reader that holds a huge backlog but reports it as not fresh.
	stale, staleEngine := newTrigger(&fakeKafkaLag{byApp: map[string]int64{"app1": 99999}, have: false})
	if err := stale.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if len(staleEngine.admitCalls) != 0 {
		t.Errorf("stale reading admitted %v; a frozen backlog must not drive capacity", staleEngine.admitCalls)
	}
	// No reader wired at all (a deployment with no Kafka triggers).
	none, noneEngine := newTrigger(nil)
	if err := none.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if len(noneEngine.admitCalls) != 0 {
		t.Errorf("nil reader admitted %v", noneEngine.admitCalls)
	}
}

// adr: 198 — kafka_lag composes with the other signals through the same
// ADR-194 arbiter, and an unreadable lag must not suppress a hot sibling.
func TestTrigger_KafkaLagCombinesWithInflight(t *testing.T) {
	store := &fakeStore{apps: []state.App{{
		ID:             "app1",
		MaxConcurrency: 20,
		ScalingPolicy: multiPolicy(0,
			state.ScalingTarget{Metric: api.ScalingMetricConcurrentRequests, Value: 4},
			state.ScalingTarget{Metric: api.ScalingMetricKafkaLag, Value: 100},
		),
	}}}
	ledger := &fakeLedger{conc: map[string]int{"app1": 2}}
	engine := &burstFakeEngine{fakeEngine: &fakeEngine{}}
	// In-flight demands ceil(5*2/4) = 3 (1 admission); lag demands
	// ceil(400/100) = 4 (2 admissions). The larger must win.
	tr := New(store, &fakeInstats{byApp: map[string]int64{"app1": 5}}, engine, ledger, Options{
		Metrics:        wire.NewOpsMetrics("schedd"),
		KafkaLagReader: &fakeKafkaLag{byApp: map[string]int64{"app1": 400}, have: true},
	})
	if err := tr.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if len(engine.burstCounts) != 1 || engine.burstCounts[0] != 2 {
		t.Fatalf("burstCounts = %v, want [2]: the lag signal's 4 desired must beat in-flight's 3",
			engine.burstCounts)
	}
}
