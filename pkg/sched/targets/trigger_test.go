package targets

import (
	"context"
	"errors"
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
	apps []state.App
}

func (f *fakeStore) ListAllApps(_ context.Context) ([]state.App, error) {
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

// fakeEngine is a minimal Engine. Records every AdmitInstance call
// and ships back a canned AdmitResult.
type fakeEngine struct {
	mu      sync.Mutex
	calls   []string
	results map[string]AdmitResult
	errs    map[string]error
}

func (e *fakeEngine) AdmitInstance(_ context.Context, appID string) (AdmitResult, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.calls = append(e.calls, appID)
	if err, ok := e.errs[appID]; ok {
		return AdmitResult{}, err
	}
	if r, ok := e.results[appID]; ok {
		return r, nil
	}
	return AdmitResult{InstanceID: "ins-" + appID}, nil
}

// EnsureWake (ADR-095): targets' trigger-local WakeOutcome mirrors
// the canned AdmitResult. The fake records a parallel call so tests
// that need to count EnsureWake vs AdmitInstance calls can do so.
func (e *fakeEngine) EnsureWake(_ context.Context, appID string) (WakeOutcome, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.calls = append(e.calls, appID)
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
	if len(engine.calls) != 1 || engine.calls[0] != "app1" {
		t.Errorf("engine.calls = %v, want [app1]", engine.calls)
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
	if len(engine.calls) != 0 {
		t.Errorf("engine.calls = %v, want [] (below target)", engine.calls)
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
	if len(engine.calls) != 0 {
		t.Errorf("engine.calls = %v, want [] (nil instats)", engine.calls)
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
	if len(engine.calls) != 0 {
		t.Errorf("engine.calls = %v, want [] (cooldown held)", engine.calls)
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
	if len(engine.calls) != 1 {
		t.Errorf("engine.calls = %d, want 1 (decide said admit, engine refused)", len(engine.calls))
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
	if len(engine.calls) != 2 {
		t.Errorf("engine.calls = %v, want [app1, app2] (engine error continues)", engine.calls)
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
	if len(engine.calls) != 1 || engine.calls[0] != "app1" {
		t.Errorf("engine.calls = %v, want [app1] (app2 filtered by metric)", engine.calls)
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
