package scaleup

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

// fakeEngine is a minimal Engine. It records the two wake surfaces
// separately because EnsureWake is idempotent while AdmitInstance is
// the scale-out primitive. A test that only checks a combined call
// count would miss accidentally wiring the wrong method.
type fakeEngine struct {
	mu              sync.Mutex
	admitCalls      []string // appIDs in AdmitInstance call order
	ensureWakeCalls []string // appIDs in EnsureWake call order
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

// sequenceScraper returns one valid sample and then a scrape error. It pins
// the fail-closed contract: a failed scrape must not reuse the previous RPS
// window as if it were current.
type sequenceScraper struct {
	mu    sync.Mutex
	first map[string]int64
	err   error
	calls int
}

func (s *sequenceScraper) Scrape(_ context.Context) (map[string]int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.calls == 0 {
		s.calls++
		return s.first, nil
	}
	s.calls++
	return nil, s.err
}

// fakeInstats is a minimal InstatsReader. Returns the per-app
// max-CPU from byCPU; nil is the no-signal case. After PR-C
// (issue #462) the interface also requires MaxInflightForApp —
// byInflight is the per-app map; nil is the no-signal case. byRPS
// exercises the optional provider-independent request-rate fallback.
type fakeInstats struct {
	byCPU      map[string]float64
	byInflight map[string]int64
	byRPS      map[string]float64
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

func (i *fakeInstats) RequestsPerSecond(appID string) (float64, bool) {
	if i == nil || i.byRPS == nil {
		return 0, false
	}
	v, ok := i.byRPS[appID]
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
	tr := New(store, nil, &fakeScraper{byApp: map[string]int64{"app1": 700}}, engine, ledger, Options{Metrics: m})
	// Pretend the previous tick already seeded cumulative=0 so
	// the first Touch sees a delta of 700 (140 app RPS over the
	// configured 5-second window, or 70 RPS/instance × 2).
	tr.ring.Touch(t0(), map[string]int64{"app1": 0})
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

func TestTrigger_UsesBoundedDesiredCapacityBurst(t *testing.T) {
	store := &fakeStore{apps: []state.App{
		{ID: "app1", AutoscaleTargetRPS: 50, MaxConcurrency: 8},
	}}
	ledger := &fakeLedger{conc: map[string]int{"app1": 1}}
	engine := &burstFakeEngine{fakeEngine: &fakeEngine{}}
	tr := New(store, nil, &fakeScraper{byApp: map[string]int64{"app1": 2500}}, engine, ledger, Options{})
	tr.ring.Touch(t0(), map[string]int64{"app1": 0})
	if err := tr.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	// 2500 requests over the configured 5-second window = 500 RPS;
	// 500 RPS / 50 target = 10 desired, but the app has one
	// instance and the trigger's per-tick burst bound is four.
	if len(engine.burstCounts) != 1 || engine.burstCounts[0] != 4 {
		t.Fatalf("burst counts = %v, want [4]", engine.burstCounts)
	}
}

func TestTrigger_UsesInstanceStatsRPSWhenPrometheusUnavailable(t *testing.T) {
	store := &fakeStore{apps: []state.App{
		{ID: "app1", AutoscaleTargetRPS: 5, MaxConcurrency: 5},
	}}
	ledger := &fakeLedger{conc: map[string]int{"app1": 1}}
	engine := &fakeEngine{}
	instats := &fakeInstats{byRPS: map[string]float64{"app1": 6}}
	tr := New(store, instats, nil, engine, ledger, Options{})

	if err := tr.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if len(engine.admitCalls) != 1 || engine.admitCalls[0] != "app1" {
		t.Fatalf("engine.admitCalls = %v, want [app1]", engine.admitCalls)
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
	tr := New(store, nil, &fakeScraper{byApp: map[string]int64{"app1": 1500}}, engine, ledger, Options{Metrics: m})
	tr.ring.Touch(t0(), map[string]int64{"app1": 0})
	if err := tr.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if len(engine.admitCalls) != 0 || len(engine.ensureWakeCalls) != 0 {
		t.Errorf("engine calls = admit:%v ensure:%v, want no calls", engine.admitCalls, engine.ensureWakeCalls)
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
	if len(engine.admitCalls) != 0 || len(engine.ensureWakeCalls) != 0 {
		t.Errorf("engine calls = admit:%v ensure:%v, want no calls", engine.admitCalls, engine.ensureWakeCalls)
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
	if len(engine.admitCalls) != 1 || engine.admitCalls[0] != "cpu-app" {
		t.Errorf("engine.admitCalls = %v, want [cpu-app]", engine.admitCalls)
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
	tr := New(store, nil, &fakeScraper{byApp: map[string]int64{"app1": 700}}, nil, ledger, Options{})
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
// check and the engine call the ledger hits the cap. AdmitInstance
// returns the typed AtCapacity result; the trigger must re-observe
// reject_at_cap and must not record an admit-RPS sample.
func TestTrigger_EngineReturnsAtCapacityObservesRejectAtCap(t *testing.T) {
	store := &fakeStore{apps: []state.App{
		{ID: "app1", AutoscaleTargetRPS: 50, MaxConcurrency: 5},
	}}
	ledger := &fakeLedger{conc: map[string]int{"app1": 2}}
	engine := &fakeEngine{
		results: map[string]AdmitResult{
			"app1": {AtCapacity: true},
		},
	}
	m := wire.NewOpsMetrics("test")
	tr := New(store, nil, &fakeScraper{byApp: map[string]int64{"app1": 700}}, engine, ledger, Options{Metrics: m})
	tr.ring.Touch(t0(), map[string]int64{"app1": 0})
	if err := tr.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if len(engine.admitCalls) != 1 {
		t.Errorf("engine.admitCalls = %d, want 1", len(engine.admitCalls))
	}
	// The admit-RPS histogram must NOT have observed (the
	// admission was rejected).
	gather, err := m.Registry().Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	foundReject := false
	for _, fam := range gather {
		if fam.GetName() == "test_scale_up_admit_rps" {
			for _, m := range fam.GetMetric() {
				if m.GetHistogram().GetSampleCount() != 0 {
					t.Errorf("scale_up_admit_rps sample count = %d, want 0 (reject should not observe)", m.GetHistogram().GetSampleCount())
				}
			}
		}
		// Gather normalizes CounterVec names by removing the _total
		// suffix; the text exposition adds it back. Accept either
		// representation so this assertion tests the label/value,
		// not the protobuf naming convention.
		if fam.GetName() != "test_scale_up_decisions" && fam.GetName() != "test_scale_up_decisions_total" {
			continue
		}
		for _, metric := range fam.GetMetric() {
			var app, outcome string
			for _, label := range metric.GetLabel() {
				switch label.GetName() {
				case "app":
					app = label.GetValue()
				case "outcome":
					outcome = label.GetValue()
				}
			}
			if app == "app1" && outcome == "reject_at_cap" {
				foundReject = true
				if metric.GetCounter().GetValue() != 1 {
					t.Errorf("reject_at_cap count = %v, want 1", metric.GetCounter().GetValue())
				}
			}
		}
	}
	if !foundReject {
		t.Error("reject_at_cap metric was not emitted")
	}
}

// TestTrigger_EngineErrorLogsNotPropagates verifies that a
// transient engine error (e.g. vmmd dial failure) is logged but
// does not propagate as a Tick error — retries are rate-limited by the
// per-app admission backoff.
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
	tr := New(store, nil, &fakeScraper{byApp: map[string]int64{"app1": 700}}, engine, ledger, Options{})
	tr.ring.Touch(t0(), map[string]int64{"app1": 0})
	if err := tr.Tick(context.Background()); err != nil {
		t.Errorf("Tick should swallow engine errors, got %v", err)
	}
}

func TestTrigger_ScrapeFailureDisablesStaleRPS(t *testing.T) {
	store := &fakeStore{apps: []state.App{
		{ID: "app1", AutoscaleTargetRPS: 50, MaxConcurrency: 5},
	}}
	ledger := &fakeLedger{conc: map[string]int{"app1": 2}}
	engine := &fakeEngine{}
	scraper := &sequenceScraper{
		first: map[string]int64{"app1": 700},
		err:   errors.New("metrics endpoint unavailable"),
	}
	tr := New(store, nil, scraper, engine, ledger, Options{})
	tr.ring.Touch(t0(), map[string]int64{"app1": 0})

	if err := tr.Tick(context.Background()); err != nil {
		t.Fatalf("first Tick: %v", err)
	}
	if err := tr.Tick(context.Background()); err != nil {
		t.Fatalf("second Tick: %v", err)
	}
	if len(engine.admitCalls) != 1 {
		t.Fatalf("engine.admitCalls = %d, want 1; stale RPS was reused after scrape failure", len(engine.admitCalls))
	}
}

func TestTrigger_AdmissionErrorBackoff(t *testing.T) {
	store := &fakeStore{apps: []state.App{
		{ID: "app1", AutoscaleTargetCPUPct: 70, MaxConcurrency: 5},
	}}
	ledger := &fakeLedger{conc: map[string]int{"app1": 2}}
	engine := &fakeEngine{errs: map[string]error{"app1": errors.New("vmmd unavailable")}}
	instats := &fakeInstats{byCPU: map[string]float64{"app1": 80}}
	tr := New(store, instats, nil, engine, ledger, Options{})

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
