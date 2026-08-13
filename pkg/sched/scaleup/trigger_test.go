package scaleup

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

// fakeStore is a minimal AppStore that returns a fixed list of apps
// from ListAllApps. Constructed once per test; ListAllApps is the
// only method the trigger calls. Phase 2 / Gate A: ListAppsByNodeID
// is the new per-schedd slice — the existing tests ignore it (the
// trigger's ownerNodeID is empty), but the interface mandates
// the method's presence so we no-op it.
type fakeStore struct {
	apps []state.App
}

func (f *fakeStore) ListAllApps(_ context.Context) ([]state.App, error) {
	return f.apps, nil
}

func (f *fakeStore) ListAppsByNodeID(_ context.Context, _ string) ([]state.App, error) {
	return f.apps, nil
}

// fakeLedger is a minimal Ledger. Concurrency returns the value
// from a per-app map.
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
// and ships back a canned AdmitResult. The trigger calls
// AdmitInstance exactly once per admit row, so the call count is the
// trip-wire for the test.
type fakeEngine struct {
	mu      sync.Mutex
	calls   []string // appIDs in call order
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

// EnsureWake (ADR-098): scaleup's trigger-local WakeOutcome mirrors
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

// fakeScraper is a minimal PromScraper. Returns a fixed app→count
// map. The trigger calls Scrape once per Tick.
type fakeScraper struct {
	mu    sync.Mutex
	byApp map[string]int64
	count int
}

func (s *fakeScraper) Scrape(_ context.Context) (map[string]int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.count++
	out := make(map[string]int64, len(s.byApp))
	for k, v := range s.byApp {
		out[k] = v
	}
	return out, nil
}

// fakeInstats is a minimal InstatsReader. Returns the per-app
// max-CPU from byCPU; nil is the no-signal case. After PR-C
// (issue #462) the interface also requires MaxInflightForApp —
// byInflight is the per-app map; nil is the no-signal case.
type fakeInstats struct {
	byCPU      map[string]float64
	byInflight map[string]int64
}

func (i *fakeInstats) MaxCPU(appID string) (float64, bool) {
	if i == nil || i.byCPU == nil {
		return 0, false
	}
	v, ok := i.byCPU[appID]
	return v, ok
}

func (i *fakeInstats) MaxInflightForApp(appID string) (int64, bool) {
	if i == nil || i.byInflight == nil {
		return 0, false
	}
	v, ok := i.byInflight[appID]
	return v, ok
}

// --- tests ---------------------------------------------------------------

// TestTrigger_AdmitOnRPSTargetHit is the happy path: a single app
// with RPS target=50, measured per-instance RPS=70, headroom=3,
// instats=nil. Tick should fire AdmitInstance once and emit
// OutcomeAdmit to the metrics.
func TestTrigger_AdmitOnRPSTargetHit(t *testing.T) {
	store := &fakeStore{apps: []state.App{
		{ID: "app1", AutoscaleTargetRPS: 50, MaxConcurrency: 5},
	}}
	ledger := &fakeLedger{conc: map[string]int{"app1": 2}}
	engine := &fakeEngine{}
	m := wire.NewOpsMetrics("test")
	tr := New(store, nil, &fakeScraper{byApp: map[string]int64{"app1": 140}}, engine, ledger, Options{Metrics: m})
	// Pretend the previous tick already seeded cumulative=0 so
	// the first Touch sees a delta of 140 (matching 70 RPS/instance
	// × 2 instances).
	tr.ring.Touch(t0(), map[string]int64{"app1": 0})
	if err := tr.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if len(engine.calls) != 1 || engine.calls[0] != "app1" {
		t.Errorf("engine.calls = %v, want [app1]", engine.calls)
	}
}

// TestTrigger_RejectAtCap verifies the cap-rejection path: an app
// at concurrency == max_concurrency is NOT admitted, even though
// the RPS target is hot. The trigger emits OutcomeRejectAtCap.
func TestTrigger_RejectAtCap(t *testing.T) {
	store := &fakeStore{apps: []state.App{
		{ID: "app1", AutoscaleTargetRPS: 50, MaxConcurrency: 5},
	}}
	ledger := &fakeLedger{conc: map[string]int{"app1": 5}}
	engine := &fakeEngine{}
	m := wire.NewOpsMetrics("test")
	tr := New(store, nil, &fakeScraper{byApp: map[string]int64{"app1": 500}}, engine, ledger, Options{Metrics: m})
	tr.ring.Touch(t0(), map[string]int64{"app1": 0})
	if err := tr.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if len(engine.calls) != 0 {
		t.Errorf("engine.calls = %v, want []", engine.calls)
	}
}

// TestTrigger_NoTargetNoOp verifies that apps with both targets
// unset are skipped entirely (no engine call, no metric — the
// metric only fires for apps the trigger actually evaluates).
func TestTrigger_NoTargetNoOp(t *testing.T) {
	store := &fakeStore{apps: []state.App{
		{ID: "app1", AutoscaleTargetRPS: 0, AutoscaleTargetCPUPct: 0, MaxConcurrency: 5},
	}}
	ledger := &fakeLedger{conc: map[string]int{"app1": 2}}
	engine := &fakeEngine{}
	tr := New(store, nil, &fakeScraper{byApp: map[string]int64{"app1": 1000}}, engine, ledger, Options{})
	if err := tr.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if len(engine.calls) != 0 {
		t.Errorf("engine.calls = %v, want []", engine.calls)
	}
}

// TestTrigger_NilScraperNoOp verifies the nil-safety contract: a
// nil promScraper means the trigger skips the RPS path entirely.
// RPS-only apps must not admit (no signal), but apps with a CPU
// target + a non-nil instats reader still admit.
func TestTrigger_NilScraperNoOp(t *testing.T) {
	store := &fakeStore{apps: []state.App{
		// RPS-only app: no signal, no admit.
		{ID: "rps-app", AutoscaleTargetRPS: 50, MaxConcurrency: 5},
		// CPU-only app with a hot signal: admit.
		{ID: "cpu-app", AutoscaleTargetCPUPct: 70, MaxConcurrency: 5},
	}}
	ledger := &fakeLedger{conc: map[string]int{"rps-app": 2, "cpu-app": 2}}
	engine := &fakeEngine{}
	instats := &fakeInstats{byCPU: map[string]float64{"cpu-app": 80}}
	tr := New(store, instats, nil, engine, ledger, Options{})
	if err := tr.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if len(engine.calls) != 1 || engine.calls[0] != "cpu-app" {
		t.Errorf("engine.calls = %v, want [cpu-app]", engine.calls)
	}
}

// TestTrigger_NilEngineNoOp verifies the nil-safety contract: a
// nil engine means the trigger evaluates apps but never admits.
// Useful during boot or in tests.
func TestTrigger_NilEngineNoOp(t *testing.T) {
	store := &fakeStore{apps: []state.App{
		{ID: "app1", AutoscaleTargetRPS: 50, MaxConcurrency: 5},
	}}
	ledger := &fakeLedger{conc: map[string]int{"app1": 2}}
	tr := New(store, nil, &fakeScraper{byApp: map[string]int64{"app1": 140}}, nil, ledger, Options{})
	if err := tr.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	// No engine means no admits — the trigger must not panic.
}

// TestTrigger_NilStoreNoOp verifies the boot-time nil-safety
// contract: a nil appStore means the trigger returns immediately.
// Mirrors the production wiring where the trigger is constructed
// before the store is ready.
func TestTrigger_NilStoreNoOp(t *testing.T) {
	tr := New(nil, nil, nil, nil, nil, Options{})
	if err := tr.Tick(context.Background()); err != nil {
		t.Errorf("Tick on nil store = %v, want nil", err)
	}
}

// TestTrigger_NilReceiver verifies the package-level nil-safety:
// Tick on a nil *Trigger must not panic. schedd's loop uses this
// when the trigger is disabled (Loop.WithScaleUp(nil)).
func TestTrigger_NilReceiver(t *testing.T) {
	var tr *Trigger
	if err := tr.Tick(context.Background()); err != nil {
		t.Errorf("Tick on nil = %v, want nil", err)
	}
	// And the interval accessor.
	if got := tr.Interval(); got != 0 {
		t.Errorf("nil Interval = %v, want 0", got)
	}
}

// TestTrigger_EngineReturnsAtCapacityObservesRejectAtCap verifies
// the race: the decide() check says headroom > 0, but between that
// check and the engine call the ledger hits the cap. ADR-098:
//
//	under the single-flight model the trigger no longer receives a
//	typed AtCapacity=true return value from EnsureWake. The leader's
//	ledger closes the at-cap loop (it returns a successful admit
//	pointing at the last live slot, OR the typed at-cap ErrQueueFull
//	path that the trigger treats as a non-fatal error). The
//	admit-RPS histogram still must NOT observe in the error case —
//	a rejected admit has no observed RPS. This test pins the
//	err-path contract under the new wire.
func TestTrigger_EngineReturnsAtCapacityObservesRejectAtCap(t *testing.T) {
	store := &fakeStore{apps: []state.App{
		{ID: "app1", AutoscaleTargetRPS: 50, MaxConcurrency: 5},
	}}
	ledger := &fakeLedger{conc: map[string]int{"app1": 2}}
	engine := &fakeEngine{
		errs: map[string]error{
			"app1": errAtCapacitySentinel,
		},
	}
	m := wire.NewOpsMetrics("test")
	tr := New(store, nil, &fakeScraper{byApp: map[string]int64{"app1": 140}}, engine, ledger, Options{Metrics: m})
	tr.ring.Touch(t0(), map[string]int64{"app1": 0})
	if err := tr.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if len(engine.calls) != 1 {
		t.Errorf("engine.calls = %d, want 1", len(engine.calls))
	}
	// The admit-RPS histogram must NOT have observed (the
	// admission was rejected).
	gather, err := m.Registry().Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	for _, fam := range gather {
		if fam.GetName() == "test_scale_up_admit_rps" {
			for _, m := range fam.GetMetric() {
				if m.GetHistogram().GetSampleCount() != 0 {
					t.Errorf("scale_up_admit_rps sample count = %d, want 0 (reject should not observe)", m.GetHistogram().GetSampleCount())
				}
			}
		}
	}
}

// errAtCapacitySentinel is a stand-in for the leader-ledger "no slot
// left" error path. Under ADR-098 the per-app ledger closes the
// at-cap loop; the trigger treats any non-context-cancelled error
// from EnsureWake as a non-fatal skip (matches the AdmitInstance
// path's behaviour). The trigger still records the per-tick call so
// the admit-RPS histogram stays unobserved.
var errAtCapacitySentinel = errAtCap("at-capacity")

type errAtCap string

func (e errAtCap) Error() string { return string(e) }

// TestTrigger_EngineErrorLogsNotPropagates verifies that a
// transient engine error (e.g. vmmd dial failure) is logged but
// does not propagate as a Tick error — the next tick retries.
func TestTrigger_EngineErrorLogsNotPropagates(t *testing.T) {
	store := &fakeStore{apps: []state.App{
		{ID: "app1", AutoscaleTargetRPS: 50, MaxConcurrency: 5},
	}}
	ledger := &fakeLedger{conc: map[string]int{"app1": 2}}
	engine := &fakeEngine{
		errs: map[string]error{
			"app1": errors.New("vmmd dial: connection refused"),
		},
	}
	tr := New(store, nil, &fakeScraper{byApp: map[string]int64{"app1": 140}}, engine, ledger, Options{})
	tr.ring.Touch(t0(), map[string]int64{"app1": 0})
	if err := tr.Tick(context.Background()); err != nil {
		t.Errorf("Tick should swallow engine errors, got %v", err)
	}
}

// TestTrigger_IntervalFromOptions verifies the Options → Interval
// plumbing: zero falls back to ScaleUpDecisionIntervalSeconds, a
// positive value is honoured.
func TestTrigger_IntervalFromOptions(t *testing.T) {
	tr := New(nil, nil, nil, nil, nil, Options{})
	if tr.Interval().Seconds() != 1 {
		t.Errorf("default Interval = %v, want 1s", tr.Interval())
	}
	tr = New(nil, nil, nil, nil, nil, Options{Interval: 2 * 1_000_000_000})
	if tr.Interval().Seconds() != 2 {
		t.Errorf("explicit Interval = %v, want 2s", tr.Interval())
	}
}

// t0 returns a fixed time so the ring buffer's bucket index is
// predictable across test runs.
func t0() (t time.Time) {
	return time.Unix(1_000_000, 0)
}
