package targets

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// --- test doubles --------------------------------------------------------

// fakeStore is a minimal AppStore. Returns a fixed list of apps from
// ListAllApps.
type fakeStore struct {
	apps     []state.App
	allCalls int
	nodeIDs  []string
}

func (f *fakeStore) ListAllApps(_ context.Context) ([]state.App, error) {
	f.allCalls++
	return f.apps, nil
}

func (f *fakeStore) ListAppsByNodeID(_ context.Context, nodeID string) ([]state.App, error) {
	f.nodeIDs = append(f.nodeIDs, nodeID)
	return f.apps, nil
}

// fakeLedger is a minimal Ledger. Concurrency returns the value from
// a per-app map.
type fakeLedger struct {
	mu   sync.Mutex
	conc map[string]int
}

func (l *fakeLedger) Concurrency(appID string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.conc == nil {
		return 0
	}
	return l.conc[appID]
}

// fakeEngine is a minimal Engine. It records the two wake surfaces
// separately because EnsureWake is idempotent while AdmitInstance is
// the scale-out primitive. A test that only checks a combined call
// count would miss accidentally wiring the wrong method.
type fakeEngine struct {
	mu              sync.Mutex
	admitCalls      []string
	ensureWakeCalls []string
	results         map[string]AdmitResult
	errs            map[string]error
}

type burstFakeEngine struct {
	*fakeEngine
	mu          sync.Mutex
	burstCounts []int
}

func (e *burstFakeEngine) AdmitInstances(_ context.Context, appID, _, _ string, count int) ([]AdmitResult, error) {
	e.mu.Lock()
	e.burstCounts = append(e.burstCounts, count)
	e.mu.Unlock()
	results := make([]AdmitResult, count)
	for i := range results {
		results[i] = AdmitResult{InstanceID: fmt.Sprintf("ins-%s-%d", appID, i)}
	}
	return results, nil
}

func (e *fakeEngine) AdmitInstance(_ context.Context, appID, _, _ string) (AdmitResult, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.admitCalls = append(e.admitCalls, appID)
	if err, ok := e.errs[appID]; ok {
		return AdmitResult{}, err
	}
	if r, ok := e.results[appID]; ok {
		return r, nil
	}
	return AdmitResult{InstanceID: "ins-" + appID}, nil
}

// EnsureWake (ADR-098) remains on the compatibility interface for
// other wake producers, but reactive scale-up must never call it.
func (e *fakeEngine) EnsureWake(_ context.Context, appID, _ string) (WakeOutcome, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.ensureWakeCalls = append(e.ensureWakeCalls, appID)
	if err, ok := e.errs[appID]; ok {
		return WakeOutcome{}, err
	}
	return WakeOutcome{InstanceID: "ins-" + appID}, nil
}

// fakeInstats is a minimal InstatsReader. Returns the per-app max
// inflight from byApp.
type fakeInstats struct {
	mu    sync.Mutex
	byApp map[string]int64
}

func (i *fakeInstats) MaxInflightForApp(appID string) (int64, bool) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.byApp == nil {
		return 0, false
	}
	v, ok := i.byApp[appID]
	return v, ok
}

// scaleUpPolicy returns a *state.ScalingPolicy wired for the
// concurrent_requests axis with the supplied target / cooldown.
func scaleUpPolicy(target float64, scaleOutCooldownS int) *state.ScalingPolicy {
	return &state.ScalingPolicy{
		Target: &state.ScalingTarget{
			Metric: "concurrent_requests",
			Value:  target,
		},
		ScaleOutCooldownS: scaleOutCooldownS,
	}
}

// --- tests ---------------------------------------------------------------

// TestTrigger_AdmitOnInflightTargetHit is the happy path: a single
// app with concurrent_requests target=1, measured per-instance
// inflight=5, headroom=3. Tick should fire AdmitInstance once.
func TestTrigger_AdmitOnInflightTargetHit(t *testing.T) {
	store := &fakeStore{apps: []state.App{{
		ID:             "app1",
		MaxConcurrency: 5,
		ScalingPolicy:  scaleUpPolicy(1.0, 60),
		LastScaleOutAt: nil, // zero → no cooldown consult
	}}}
	ledger := &fakeLedger{conc: map[string]int{"app1": 2}}
	instats := &fakeInstats{byApp: map[string]int64{"app1": 5}}
	engine := &fakeEngine{}
	tr := New(store, instats, engine, ledger, Options{Metrics: wire.NewOpsMetrics("schedd")})
	if err := tr.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if len(engine.admitCalls) != 1 || engine.admitCalls[0] != "app1" {
		t.Errorf("engine.admitCalls = %v, want [app1]", engine.admitCalls)
	}
	if len(engine.ensureWakeCalls) != 0 {
		t.Errorf("engine.ensureWakeCalls = %v, want []: scale-out must bypass EnsureWake", engine.ensureWakeCalls)
	}
}

// TestTrigger_UsesOwnerNodeSlice verifies that a multi-node schedd reads
// only the apps assigned to its durable owner node. Without this shard,
// every schedd evaluates every app and multiple nodes can race to admit
// duplicate capacity for the same workload.
func TestTrigger_UsesOwnerNodeSlice(t *testing.T) {
	store := &fakeStore{apps: []state.App{{
		ID:             "app1",
		MaxConcurrency: 5,
		ScalingPolicy:  scaleUpPolicy(1.0, 60),
	}}}
	ledger := &fakeLedger{conc: map[string]int{"app1": 2}}
	instats := &fakeInstats{byApp: map[string]int64{"app1": 5}}
	engine := &fakeEngine{}
	tr := New(store, instats, engine, ledger, Options{Metrics: wire.NewOpsMetrics("schedd")})
	tr.WithOwnerNodeID("compute-2")

	if err := tr.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if store.allCalls != 0 {
		t.Errorf("ListAllApps calls = %d, want 0", store.allCalls)
	}
	if len(store.nodeIDs) != 1 || store.nodeIDs[0] != "compute-2" {
		t.Errorf("ListAppsByNodeID calls = %v, want [compute-2]", store.nodeIDs)
	}
	if len(engine.admitCalls) != 1 || len(engine.ensureWakeCalls) != 0 {
		t.Errorf("engine calls = admit:%v wake:%v, want one admit and no wake", engine.admitCalls, engine.ensureWakeCalls)
	}
}

func TestTrigger_UsesBoundedDesiredCapacityBurst(t *testing.T) {
	store := &fakeStore{apps: []state.App{{
		ID:             "app1",
		MaxConcurrency: 8,
		ScalingPolicy:  scaleUpPolicy(10, 0),
	}}}
	ledger := &fakeLedger{conc: map[string]int{"app1": 2}}
	instats := &fakeInstats{byApp: map[string]int64{"app1": 35}}
	engine := &burstFakeEngine{fakeEngine: &fakeEngine{}}
	tr := New(store, instats, engine, ledger, Options{})
	if err := tr.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	// 2 instances × 35 inflight / 10 target = 7 desired, so
	// five admissions are needed; the trigger caps this tick at four.
	if len(engine.burstCounts) != 1 || engine.burstCounts[0] != 4 {
		t.Fatalf("burst counts = %v, want [4]", engine.burstCounts)
	}
}

// TestTrigger_NoSignalOnInfightBelowTarget verifies the trigger does
// NOT admit when per-instance inflight is below the customer's
// target (strict > means measured=target falls through to no_signal).
func TestTrigger_NoSignalOnInflightBelowTarget(t *testing.T) {
	store := &fakeStore{apps: []state.App{{
		ID:             "app1",
		MaxConcurrency: 5,
		ScalingPolicy:  scaleUpPolicy(5.0, 60),
	}}}
	ledger := &fakeLedger{conc: map[string]int{"app1": 2}}
	instats := &fakeInstats{byApp: map[string]int64{"app1": 3}} // below target
	engine := &fakeEngine{}
	tr := New(store, instats, engine, ledger, Options{Metrics: wire.NewOpsMetrics("schedd")})
	if err := tr.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if len(engine.admitCalls) != 0 || len(engine.ensureWakeCalls) != 0 {
		t.Errorf("engine calls = admit:%v ensure:%v, want no calls (below target)", engine.admitCalls, engine.ensureWakeCalls)
	}
}

// TestTrigger_NoSignalWithoutInflightReader verifies that a nil
// instats (degraded mode) turns every app into no_signal and the
// trigger does not call AdmitInstance.
func TestTrigger_NoSignalWithoutInflightReader(t *testing.T) {
	store := &fakeStore{apps: []state.App{{
		ID:             "app1",
		MaxConcurrency: 5,
		ScalingPolicy:  scaleUpPolicy(1.0, 60),
	}}}
	ledger := &fakeLedger{conc: map[string]int{"app1": 2}}
	engine := &fakeEngine{}
	tr := New(store, nil, engine, ledger, Options{Metrics: wire.NewOpsMetrics("schedd")})
	if err := tr.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if len(engine.admitCalls) != 0 || len(engine.ensureWakeCalls) != 0 {
		t.Errorf("engine calls = admit:%v ensure:%v, want no calls (nil instats)", engine.admitCalls, engine.ensureWakeCalls)
	}
}

// TestTrigger_CooldownHeld verifies the cooldown consult: an app
// with Concurrency > 0 AND a freshly-stamped LastScaleOutAt AND a
// non-zero cooldown MUST NOT admit (even when target is met).
func TestTrigger_CooldownHeld(t *testing.T) {
	now := time.Now()
	stamp := now.Add(-1 * time.Second)
	store := &fakeStore{apps: []state.App{{
		ID:             "app1",
		MaxConcurrency: 5,
		ScalingPolicy:  scaleUpPolicy(1.0, 60),
		LastScaleOutAt: &stamp,
	}}}
	ledger := &fakeLedger{conc: map[string]int{"app1": 2}}
	instats := &fakeInstats{byApp: map[string]int64{"app1": 10}} // hot
	engine := &fakeEngine{}
	tr := New(store, instats, engine, ledger, Options{Metrics: wire.NewOpsMetrics("schedd")})
	if err := tr.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if len(engine.admitCalls) != 0 || len(engine.ensureWakeCalls) != 0 {
		t.Errorf("engine calls = admit:%v ensure:%v, want no calls (cooldown held)", engine.admitCalls, engine.ensureWakeCalls)
	}
}

// TestTrigger_AtCapacityReAdmitReObserved verifies the two-step
// metric emission: decide() says admit, the engine returns
// AtCapacity=true, the trigger re-observes the rejection on the
// same call.
func TestTrigger_AtCapacityReAdmitReObserved(t *testing.T) {
	store := &fakeStore{apps: []state.App{{
		ID:             "app1",
		MaxConcurrency: 5,
		ScalingPolicy:  scaleUpPolicy(1.0, 60),
	}}}
	ledger := &fakeLedger{conc: map[string]int{"app1": 2}}
	instats := &fakeInstats{byApp: map[string]int64{"app1": 10}}
	engine := &fakeEngine{results: map[string]AdmitResult{"app1": {AtCapacity: true}}}
	tr := New(store, instats, engine, ledger, Options{Metrics: wire.NewOpsMetrics("schedd")})
	if err := tr.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	// The trigger called AdmitInstance (decide said admit) but
	// the engine refused — call count is still 1.
	if len(engine.admitCalls) != 1 {
		t.Errorf("engine.admitCalls = %d, want 1 (decide said admit, engine refused)", len(engine.admitCalls))
	}
	if len(engine.ensureWakeCalls) != 0 {
		t.Errorf("engine.ensureWakeCalls = %v, want []: scale-out must bypass EnsureWake", engine.ensureWakeCalls)
	}
}

// TestTrigger_EngineErrorContinues verifies an AdmitInstance error
// does not abort the loop. The trigger logs and continues to the
// next app.
func TestTrigger_EngineErrorContinues(t *testing.T) {
	store := &fakeStore{apps: []state.App{
		{ID: "app1", MaxConcurrency: 5, ScalingPolicy: scaleUpPolicy(1.0, 60)},
		{ID: "app2", MaxConcurrency: 5, ScalingPolicy: scaleUpPolicy(1.0, 60)},
	}}
	ledger := &fakeLedger{conc: map[string]int{"app1": 1, "app2": 1}}
	instats := &fakeInstats{byApp: map[string]int64{"app1": 5, "app2": 5}}
	engine := &fakeEngine{errs: map[string]error{"app1": errors.New("boom")}}
	tr := New(store, instats, engine, ledger, Options{Metrics: wire.NewOpsMetrics("schedd")})
	if err := tr.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	// app1 errored (logged + skipped), app2 admitted — total 1
	// successful call.
	if len(engine.admitCalls) != 2 {
		t.Errorf("engine.admitCalls = %v, want [app1, app2] (engine error continues)", engine.admitCalls)
	}
}

func TestTrigger_AdmissionErrorBackoff(t *testing.T) {
	store := &fakeStore{apps: []state.App{
		{ID: "app1", MaxConcurrency: 5, ScalingPolicy: scaleUpPolicy(1.0, 60)},
	}}
	ledger := &fakeLedger{conc: map[string]int{"app1": 1}}
	instats := &fakeInstats{byApp: map[string]int64{"app1": 5}}
	engine := &fakeEngine{errs: map[string]error{"app1": errors.New("vmmd unavailable")}}
	tr := New(store, instats, engine, ledger, Options{})

	if err := tr.Tick(context.Background()); err != nil {
		t.Fatalf("first Tick: %v", err)
	}
	if err := tr.Tick(context.Background()); err != nil {
		t.Fatalf("second Tick: %v", err)
	}
	if len(engine.admitCalls) != 1 {
		t.Fatalf("engine.admitCalls = %d, want 1 while retry backoff is active", len(engine.admitCalls))
	}
}

// TestTrigger_FiltersNonConcurrentRequestsApps verifies the
// target.metric filter: apps whose Target.Metric !=
// "concurrent_requests" are NOT processed by the targets trigger
// (the scaleup package handles them).
func TestTrigger_FiltersNonConcurrentRequestsApps(t *testing.T) {
	store := &fakeStore{apps: []state.App{
		{ID: "app1", MaxConcurrency: 5, ScalingPolicy: scaleUpPolicy(1.0, 60)},
		{ID: "app2", MaxConcurrency: 5, ScalingPolicy: &state.ScalingPolicy{
			Target: &state.ScalingTarget{Metric: "rps", Value: 50},
		}},
	}}
	ledger := &fakeLedger{conc: map[string]int{"app1": 1, "app2": 1}}
	instats := &fakeInstats{byApp: map[string]int64{"app1": 5, "app2": 5}}
	engine := &fakeEngine{}
	tr := New(store, instats, engine, ledger, Options{Metrics: wire.NewOpsMetrics("schedd")})
	if err := tr.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if len(engine.admitCalls) != 1 || engine.admitCalls[0] != "app1" {
		t.Errorf("engine.admitCalls = %v, want [app1] (app2 filtered by metric)", engine.admitCalls)
	}
}

// TestTrigger_NilSafe verifies the nil-receiver contract: Tick on a
// nil *Trigger is a no-op.
func TestTrigger_NilSafe(t *testing.T) {
	var tr *Trigger
	if err := tr.Tick(context.Background()); err != nil {
		t.Errorf("nil.Tick = %v, want nil", err)
	}
	if got := tr.Interval(); got != 0 {
		t.Errorf("nil.Interval = %v, want 0", got)
	}
}

// TestTrigger_StoreErrorBubbles verifies that an appStore outage
// surfaces as an error from Tick so the loop can log it.
func TestTrigger_StoreErrorBubbles(t *testing.T) {
	store := &errStore{}
	instats := &fakeInstats{}
	tr := New(store, instats, &fakeEngine{}, &fakeLedger{}, Options{})
	if err := tr.Tick(context.Background()); err == nil {
		t.Error("Tick on errStore returned nil, want error")
	}
}

// errStore is a fakeStore variant whose ListAllApps always errors.
type errStore struct{}

func (e *errStore) ListAllApps(_ context.Context) ([]state.App, error) {
	return nil, errors.New("store down")
}

func (e *errStore) ListAppsByNodeID(_ context.Context, _ string) ([]state.App, error) {
	return nil, errors.New("store down")
}
