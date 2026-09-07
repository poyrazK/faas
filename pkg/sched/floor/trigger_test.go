package floor

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// --- test doubles --------------------------------------------------------

// fakeStore is a minimal AppStore. ListAllApps returns a fixed list;
// ListAppsByNodeID filters the list by node id.
type fakeStore struct {
	apps    []state.App
	listErr error
}

func (f *fakeStore) ListAllApps(_ context.Context) ([]state.App, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.apps, nil
}

func (f *fakeStore) ListAppsByNodeID(_ context.Context, _ string) ([]state.App, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.apps, nil
}

// fakeLedger is a minimal Ledger. Concurrency returns the value from
// a per-app map; ResidentRAM/HeadroomMB return fixed values. Tests
// pass headroom=47_600 explicitly to keep the §6.2-2 ceiling
// pre-check non-blocking unless the test explicitly tightens it.
type fakeLedger struct {
	mu          sync.Mutex
	conc        map[string]int
	depConc     map[string]int // key: appID+"\x00"+deploymentID
	residentRAM int
	headroom    int
}

func (l *fakeLedger) Concurrency(appID string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.conc[appID]
}

func (l *fakeLedger) ConcurrencyForDeployment(appID, deploymentID string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.depConc[appID+"\x00"+deploymentID]
}

func (l *fakeLedger) ResidentRAM() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.residentRAM
}

func (l *fakeLedger) HeadroomMB() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.headroom
}

// fakeEngine is a minimal Engine. Records every AdmitInstance call
// and ships back a canned AdmitResult or canned error per app.
type fakeEngine struct {
	mu      sync.Mutex
	calls   []string
	results map[string]AdmitResult
	errs    map[string]error
}

func (e *fakeEngine) AdmitInstance(_ context.Context, appID, _, _ string) (AdmitResult, error) {
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

// EnsureWake (ADR-098): floor's trigger-local WakeOutcome mirrors
// the canned AdmitResult. The fake records a parallel call so tests
// that need to count EnsureWake vs AdmitInstance calls can do so.
// Honours canned results (instance_id echo) so tests that pre-load
// a specific InstanceID — e.g. TestTick_AuditorEmitsFloorWake
// pinning "iid-xyz" — keep working unchanged.
func (e *fakeEngine) EnsureWake(_ context.Context, appID, _ string) (WakeOutcome, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.calls = append(e.calls, appID)
	if err, ok := e.errs[appID]; ok {
		return WakeOutcome{}, err
	}
	if r, ok := e.results[appID]; ok {
		return WakeOutcome{InstanceID: r.InstanceID}, nil
	}
	return WakeOutcome{InstanceID: "ins-" + appID}, nil
}

// AdmitInstanceForDeployment mirrors AdmitInstance on the
// per-deployment entry point (issue #557 closure / ADR-072). The
// trigger's per-deployment walk calls this; the per-app walk still
// calls AdmitInstance. The fake records both with the same
// `appID|deploymentID` shape so the tests can assert either path.
func (e *fakeEngine) AdmitInstanceForDeployment(_ context.Context, appID, deploymentID, _, _ string) (AdmitResult, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.calls = append(e.calls, appID+"|"+deploymentID)
	key := appID + "|" + deploymentID
	if err, ok := e.errs[key]; ok {
		return AdmitResult{}, err
	}
	if r, ok := e.results[key]; ok {
		return r, nil
	}
	return AdmitResult{InstanceID: "ins-" + appID + "-" + deploymentID}, nil
}

// fakePlanResolver returns the canned plan for an account id.
type fakePlanResolver struct {
	plans map[string]api.Plan
}

func (p *fakePlanResolver) ResolvePlan(_ context.Context, accountID string) (api.Plan, bool) {
	pl, ok := p.plans[accountID]
	return pl, ok
}

// fakeAuditor records every Emit call for audit-kind assertions.
type fakeAuditor struct {
	mu        sync.Mutex
	emissions []fakeEmission
}

type fakeEmission struct {
	kind      string
	accountID string
	data      map[string]any
}

func (a *fakeAuditor) Emit(_ context.Context, kind string, accountID *string, data map[string]any) {
	a.mu.Lock()
	defer a.mu.Unlock()
	acct := ""
	if accountID != nil {
		acct = *accountID
	}
	a.emissions = append(a.emissions, fakeEmission{kind, acct, data})
}

// errStore is a fakeStore variant whose ListAllApps always errors.
type errStore struct{}

func (e *errStore) ListAllApps(_ context.Context) ([]state.App, error) {
	return nil, errors.New("store down")
}

func (e *errStore) ListAppsByNodeID(_ context.Context, _ string) ([]state.App, error) {
	return nil, errors.New("store down")
}

// --- helpers -------------------------------------------------------------

// floorApp returns a state.App wired for the floor trigger with the
// supplied min_instances on the legacy column. AccountID defaults to
// "acct1" so tests stay short. Plan is resolved out-of-band by
// fakePlanResolver — it's not on state.App.
func floorApp(id string, _ api.Plan, minInstances int) state.App {
	return state.App{
		ID:            id,
		AccountID:     "acct1",
		MinInstances:  minInstances,
		RAMMB:         256,
		WorkloadClass: state.WorkloadClassHTTP,
	}
}

// --- tests ---------------------------------------------------------------

// TestTick_AdmitsUpToFloor is the issue #557 acceptance criterion
// #1: an app with min_instances=2 and 0 running instances MUST be
// admitted twice across consecutive ticks until the floor is met.
func TestTick_AdmitsUpToFloor(t *testing.T) {
	store := &fakeStore{apps: []state.App{floorApp("app1", api.PlanHobby, 2)}}
	ledger := &fakeLedger{conc: map[string]int{"app1": 0}, headroom: 47_600}
	engine := &fakeEngine{}
	resolver := &fakePlanResolver{plans: map[string]api.Plan{"acct1": api.PlanHobby}}
	auditor := &fakeAuditor{}
	tr := New(store, nil, ledger, engine, Options{
		Metrics:      wire.NewOpsMetrics("schedd"),
		Auditor:      auditor,
		PlanResolver: resolver,
	})

	// Tick 1: floor=2, conc=0 → admit once → engine creates instance.
	if err := tr.Tick(context.Background()); err != nil {
		t.Fatalf("Tick 1: %v", err)
	}
	if len(engine.calls) != 1 || engine.calls[0] != "app1" {
		t.Errorf("Tick 1 engine.calls = %v, want [app1]", engine.calls)
	}
	if len(auditor.emissions) != 1 || auditor.emissions[0].kind != "floor.wake" {
		t.Errorf("Tick 1 auditor emissions = %+v, want one floor.wake", auditor.emissions)
	}

	// Simulate the engine's effect on the ledger: conc now 1.
	ledger.conc["app1"] = 1
	if err := tr.Tick(context.Background()); err != nil {
		t.Fatalf("Tick 2: %v", err)
	}
	if len(engine.calls) != 2 {
		t.Errorf("Tick 2 engine.calls = %v, want second app1 admit", engine.calls)
	}

	// Now conc=2 → floor met → no admit.
	ledger.conc["app1"] = 2
	if err := tr.Tick(context.Background()); err != nil {
		t.Fatalf("Tick 3: %v", err)
	}
	if len(engine.calls) != 2 {
		t.Errorf("Tick 3 engine.calls = %v, want no admits (floor met)", engine.calls)
	}
}

// TestTick_FreePlanDisabled verifies the plan gate: a Free-plan app
// is silently dropped even when the customer wrote min_instances=2.
func TestTick_FreePlanDisabled(t *testing.T) {
	store := &fakeStore{apps: []state.App{floorApp("app1", api.PlanFree, 2)}}
	ledger := &fakeLedger{conc: map[string]int{"app1": 0}, headroom: 47_600}
	engine := &fakeEngine{}
	resolver := &fakePlanResolver{plans: map[string]api.Plan{"acct1": api.PlanFree}}
	tr := New(store, nil, ledger, engine, Options{
		Metrics:      wire.NewOpsMetrics("schedd"),
		PlanResolver: resolver,
	})
	if err := tr.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if len(engine.calls) != 0 {
		t.Errorf("engine.calls = %v, want [] (Free plan disabled)", engine.calls)
	}
}

// TestTick_WorkerClassDisabled verifies the workload-class gate:
// worker-class apps never get floor wakes even when the customer
// set min_instances.
func TestTick_WorkerClassDisabled(t *testing.T) {
	app := floorApp("app1", api.PlanHobby, 2)
	app.WorkloadClass = state.WorkloadClassWorker
	store := &fakeStore{apps: []state.App{app}}
	ledger := &fakeLedger{conc: map[string]int{"app1": 0}, headroom: 47_600}
	engine := &fakeEngine{}
	resolver := &fakePlanResolver{plans: map[string]api.Plan{"acct1": api.PlanHobby}}
	tr := New(store, nil, ledger, engine, Options{
		Metrics:      wire.NewOpsMetrics("schedd"),
		PlanResolver: resolver,
	})
	if err := tr.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if len(engine.calls) != 0 {
		t.Errorf("engine.calls = %v, want [] (worker class disabled)", engine.calls)
	}
}

// TestTick_BillableExceedsHeadroom verifies the §6.2-2 ceiling
// defense: an app whose billable RAM (RAMMB + 8 MB overhead) alone
// exceeds current headroom must yield without calling
// AdmitInstance. The pre-check is the BillableRAMMB > headroom
// guard at trigger.go (v1 "yield to headroom" per ADR-071
// §Decision 3); a future FAAS_FLOOR_RESERVED_MB env knob may widen
// this to a stricter absolute-ceiling check.
func TestTick_BillableExceedsHeadroom(t *testing.T) {
	app := floorApp("app1", api.PlanPro, 1)
	app.RAMMB = 1024 // Pro ceiling includes 8 MB overhead → 1032 MB.
	store := &fakeStore{apps: []state.App{app}}
	// Headroom smaller than the projected admit.
	ledger := &fakeLedger{conc: map[string]int{"app1": 0}, residentRAM: 47_000, headroom: 600}
	engine := &fakeEngine{}
	resolver := &fakePlanResolver{plans: map[string]api.Plan{"acct1": api.PlanPro}}
	tr := New(store, nil, ledger, engine, Options{
		Metrics:      wire.NewOpsMetrics("schedd"),
		PlanResolver: resolver,
	})
	if err := tr.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if len(engine.calls) != 0 {
		t.Errorf("engine.calls = %v, want [] (RAM ceiling)", engine.calls)
	}
}

// TestTick_AtCapacityRecordedNotErrored verifies the bifurcation:
// the engine returning AtCapacity=true is SUCCESS (no FAILED row,
// no backoff), but the trigger records the at_capacity outcome.
func TestTick_AtCapacityRecordedNotErrored(t *testing.T) {
	store := &fakeStore{apps: []state.App{floorApp("app1", api.PlanHobby, 2)}}
	ledger := &fakeLedger{conc: map[string]int{"app1": 1}, headroom: 47_600}
	engine := &fakeEngine{results: map[string]AdmitResult{"app1": {AtCapacity: true}}}
	resolver := &fakePlanResolver{plans: map[string]api.Plan{"acct1": api.PlanHobby}}
	tr := New(store, nil, ledger, engine, Options{
		Metrics:      wire.NewOpsMetrics("schedd"),
		PlanResolver: resolver,
	})
	if err := tr.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if len(engine.calls) != 1 {
		t.Errorf("engine.calls = %v, want [app1]", engine.calls)
	}
	// Second tick must NOT be in backoff (AtCapacity is success).
	ledger.conc["app1"] = 0
	if err := tr.Tick(context.Background()); err != nil {
		t.Fatalf("Tick 2: %v", err)
	}
	if len(engine.calls) != 2 {
		t.Errorf("Tick 2 engine.calls = %v, want second admit (AtCapacity cleared backoff)", engine.calls)
	}
}

// TestTick_EngineErrorRecordsBackoff verifies the per-app exponential
// backoff: a non-nil error from AdmitInstance must put the app to
// sleep; the next tick within the window must NOT call the engine.
func TestTick_EngineErrorRecordsBackoff(t *testing.T) {
	store := &fakeStore{apps: []state.App{floorApp("app1", api.PlanHobby, 1)}}
	ledger := &fakeLedger{conc: map[string]int{"app1": 0}, headroom: 47_600}
	engine := &fakeEngine{errs: map[string]error{"app1": errors.New("vmmd unreachable")}}
	resolver := &fakePlanResolver{plans: map[string]api.Plan{"acct1": api.PlanHobby}}
	tr := New(store, nil, ledger, engine, Options{
		Metrics:      wire.NewOpsMetrics("schedd"),
		PlanResolver: resolver,
	})
	if err := tr.Tick(context.Background()); err != nil {
		t.Fatalf("Tick 1: %v", err)
	}
	if len(engine.calls) != 1 {
		t.Errorf("Tick 1 engine.calls = %v, want [app1]", engine.calls)
	}
	// Immediate retry → backoff must block.
	if err := tr.Tick(context.Background()); err != nil {
		t.Fatalf("Tick 2: %v", err)
	}
	if len(engine.calls) != 1 {
		t.Errorf("Tick 2 engine.calls = %v, want [] (backoff held)", engine.calls)
	}
	// Clear the backoff by hand so the assertion about success is
	// deterministic.
	tr.recordSuccess("app1")
	engine.errs = nil
	if err := tr.Tick(context.Background()); err != nil {
		t.Fatalf("Tick 3: %v", err)
	}
	if len(engine.calls) != 2 {
		t.Errorf("Tick 3 engine.calls = %v, want second admit (backoff cleared)", engine.calls)
	}
}

// TestTick_ScaleOutCooldownHeld verifies the per-app cooldown check
// behaves like the engine: LastScaleOutAt within ScaleOutCooldownS
// blocks the admit.
func TestTick_ScaleOutCooldownHeld(t *testing.T) {
	now := time.Now()
	stamp := now.Add(-1 * time.Second)
	app := floorApp("app1", api.PlanHobby, 2)
	app.LastScaleOutAt = &stamp
	app.ScalingPolicy = &state.ScalingPolicy{ScaleOutCooldownS: 60}
	store := &fakeStore{apps: []state.App{app}}
	ledger := &fakeLedger{conc: map[string]int{"app1": 1}}
	engine := &fakeEngine{}
	resolver := &fakePlanResolver{plans: map[string]api.Plan{"acct1": api.PlanHobby}}
	tr := New(store, nil, ledger, engine, Options{
		Metrics:      wire.NewOpsMetrics("schedd"),
		PlanResolver: resolver,
	})
	if err := tr.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if len(engine.calls) != 0 {
		t.Errorf("engine.calls = %v, want [] (cooldown held)", engine.calls)
	}
}

// TestTick_OwnerNodeIDRoutesToListAppsByNodeID verifies that
// WithOwnerNodeID flips the trigger from ListAllApps to
// ListAppsByNodeID. Both fakes are instrumented to expose which
// method schedd hit.
func TestTick_OwnerNodeIDRoutesToListAppsByNodeID(t *testing.T) {
	store := &instrumentedStore{apps: []state.App{floorApp("app1", api.PlanHobby, 2)}}
	ledger := &fakeLedger{conc: map[string]int{"app1": 0}, headroom: 47_600}
	engine := &fakeEngine{}
	resolver := &fakePlanResolver{plans: map[string]api.Plan{"acct1": api.PlanHobby}}
	tr := New(store, nil, ledger, engine, Options{
		Metrics:      wire.NewOpsMetrics("schedd"),
		PlanResolver: resolver,
	})

	// Unsharded → ListAllApps.
	if err := tr.Tick(context.Background()); err != nil {
		t.Fatalf("Tick (unsharded): %v", err)
	}
	if !store.listAllCalls {
		t.Error("unsharded Tick did not call ListAllApps")
	}
	if store.listByIDCalls != 0 {
		t.Errorf("unsharded Tick called ListAppsByNodeID %d times, want 0", store.listByIDCalls)
	}

	// Sharded → ListAppsByNodeID.
	tr.WithOwnerNodeID("node-1")
	if err := tr.Tick(context.Background()); err != nil {
		t.Fatalf("Tick (sharded): %v", err)
	}
	if store.listByIDCalls != 1 {
		t.Errorf("sharded Tick called ListAppsByNodeID %d times, want 1", store.listByIDCalls)
	}
}

// instrumentedStore records which method the trigger called. Apps
// are returned from either method so the assertion is purely about
// the routing decision.
type instrumentedStore struct {
	apps          []state.App
	listAllCalls  bool
	listByIDCalls int
}

func (s *instrumentedStore) ListAllApps(_ context.Context) ([]state.App, error) {
	s.listAllCalls = true
	return s.apps, nil
}

func (s *instrumentedStore) ListAppsByNodeID(_ context.Context, _ string) ([]state.App, error) {
	s.listByIDCalls++
	return s.apps, nil
}

// TestTick_NilSafe verifies the nil-receiver contract: Tick on a nil
// *Trigger is a no-op.
func TestTick_NilSafe(t *testing.T) {
	var tr *Trigger
	if err := tr.Tick(context.Background()); err != nil {
		t.Errorf("nil.Tick = %v, want nil", err)
	}
	if got := tr.Interval(); got != 0 {
		t.Errorf("nil.Interval = %v, want 0", got)
	}
}

// TestTick_StoreErrorBubbles verifies that an appStore outage
// surfaces as an error from Tick so the loop can log it.
func TestTick_StoreErrorBubbles(t *testing.T) {
	tr := New(&errStore{}, nil, &fakeLedger{}, &fakeEngine{}, Options{Metrics: wire.NewOpsMetrics("schedd")})
	if err := tr.Tick(context.Background()); err == nil {
		t.Error("Tick on errStore returned nil, want error")
	}
}

// TestTick_NilLedgerIsSafe verifies the trigger's defensive posture
// when downstream dependencies are nil (load-bearing for schedd's
// early-boot wiring). The trigger no-ops rather than panics.
func TestTick_NilLedgerIsSafe(t *testing.T) {
	store := &fakeStore{apps: []state.App{floorApp("app1", api.PlanHobby, 1)}}
	engine := &fakeEngine{}
	resolver := &fakePlanResolver{plans: map[string]api.Plan{"acct1": api.PlanHobby}}
	tr := New(store, nil, nil, engine, Options{
		Metrics:      wire.NewOpsMetrics("schedd"),
		PlanResolver: resolver,
	})
	if err := tr.Tick(context.Background()); err != nil {
		t.Fatalf("Tick with nil ledger: %v", err)
	}
	if len(engine.calls) != 1 {
		t.Errorf("engine.calls = %v, want [app1] (nil ledger treated as conc=0)", engine.calls)
	}
}

// TestTick_NilPlanResolverDefaultsToFree verifies the resolver
// default: missing plan → Free → OutcomeDisabled. Customers whose
// account lookup races the trigger see a clean disabled-outcome path
// rather than a panic.
func TestTick_NilPlanResolverDefaultsToFree(t *testing.T) {
	store := &fakeStore{apps: []state.App{floorApp("app1", api.PlanHobby, 2)}}
	ledger := &fakeLedger{conc: map[string]int{"app1": 0}, headroom: 47_600}
	engine := &fakeEngine{}
	tr := New(store, nil, ledger, engine, Options{Metrics: wire.NewOpsMetrics("schedd")})
	if err := tr.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if len(engine.calls) != 0 {
		t.Errorf("engine.calls = %v, want [] (nil resolver → Free → disabled)", engine.calls)
	}
}

// TestTick_AuditorEmitsFloorWake verifies the audit emission contract
// on the happy path. The data payload includes app_id, floor,
// concurrency_before, and wake_id (= AdmitResult.InstanceID).
func TestTick_AuditorEmitsFloorWake(t *testing.T) {
	store := &fakeStore{apps: []state.App{floorApp("app1", api.PlanHobby, 2)}}
	ledger := &fakeLedger{conc: map[string]int{"app1": 0}, headroom: 47_600}
	engine := &fakeEngine{results: map[string]AdmitResult{"app1": {InstanceID: "iid-xyz"}}}
	resolver := &fakePlanResolver{plans: map[string]api.Plan{"acct1": api.PlanHobby}}
	auditor := &fakeAuditor{}
	tr := New(store, nil, ledger, engine, Options{
		Metrics:      wire.NewOpsMetrics("schedd"),
		Auditor:      auditor,
		PlanResolver: resolver,
	})
	if err := tr.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if len(auditor.emissions) != 1 {
		t.Fatalf("auditor.emissions = %+v, want exactly 1", auditor.emissions)
	}
	em := auditor.emissions[0]
	if em.kind != "floor.wake" {
		t.Errorf("kind = %q, want floor.wake", em.kind)
	}
	if em.accountID != "acct1" {
		t.Errorf("accountID = %q, want acct1", em.accountID)
	}
	if em.data["app_id"] != "app1" {
		t.Errorf("data[app_id] = %v, want app1", em.data["app_id"])
	}
	if em.data["wake_id"] != "iid-xyz" {
		t.Errorf("data[wake_id] = %v, want iid-xyz", em.data["wake_id"])
	}
}

// --- per-deployment tick coverage (issue #557 / ADR-072) ----------------

// fakeDeploymentStore is the production-side seam the per-deployment
// floor sweep reads against. Mirrors the trigger.DeploymentStore
// interface methods 1:1. The default zero value is "no deployments
// configured" — every method either returns an empty slice / zero
// value or, for the error knobs, returns nil unless the test
// specifically opted in.
type fakeDeploymentStore struct {
	mu sync.Mutex

	deps []state.Deployment
	apps map[string]state.App

	listAllErr   error
	listByNIDErr error
	appByIDErr   map[string]error // keyed by app id; nil = success

	listAllCalls  bool
	listByIDCalls int

	// Which nodeID was last passed to ListDeploymentsByNodeID. The
	// OwnerNodeIDRoutesToListDeploymentsByNodeID test asserts the
	// trigger forwards t.ownerNodeID unchanged.
	lastNodeID string
}

func (s *fakeDeploymentStore) ListAllDeployments(_ context.Context) ([]state.Deployment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.listAllCalls = true
	if s.listAllErr != nil {
		return nil, s.listAllErr
	}
	return s.deps, nil
}

func (s *fakeDeploymentStore) ListDeploymentsByNodeID(_ context.Context, nodeID string) ([]state.Deployment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.listByIDCalls++
	s.lastNodeID = nodeID
	if s.listByNIDErr != nil {
		return nil, s.listByNIDErr
	}
	return s.deps, nil
}

func (s *fakeDeploymentStore) AppByID(_ context.Context, id string) (state.App, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err, ok := s.appByIDErr[id]; ok && err != nil {
		return state.App{}, err
	}
	return s.apps[id], nil
}

func (s *fakeDeploymentStore) ConcurrencyForDeployment(_ context.Context, appID, deploymentID string) (int, error) {
	// The Trigger delegates to the Ledger (fakeLedger.depConc) for
	// the per-deployment concurrency; this method is part of the
	// DeploymentStore interface so an alternate implementation
	// (e.g. a Postgres-backed DeploymentStore) could read its own
	// view. For the trigger's tickPerDeployment path the field is
	// not consulted (see tickPerDeployment:462-465) — the value
	// flows through t.ledger.ConcurrencyForDeployment. Returning
	// 0 keeps the interface satisfied without affecting assertions.
	return 0, nil
}

// floorDeployment constructs a per-deployment row for the tests.
// The parent app is implied via d.AppID — the test wires both
// floorDeployment{d1 of app1} and floorApp("app1", ...) so the
// trigger's AppByID lookup finds the right row.
func floorDeployment(id, appID string, minInstances int) state.Deployment {
	return state.Deployment{
		ID:           id,
		AppID:        appID,
		Kind:         state.DeploymentKindImage,
		Status:       state.DeployLive,
		MinInstances: minInstances,
	}
}

// withDeploymentStore is a tiny helper that mirrors New()'s
// signature but threads a non-nil deploymentStore through it. The
// legacy New(store, nil, ...) call shape silently falls back to
// tickPerApp; the production wiring at cmd/schedd/main.go:1138 uses
// this non-nil shape, which is what every per-deployment test must
// hit to be load-bearing.
func withDeploymentStore(t *testing.T, appStore AppStore, depStore DeploymentStore, ledger Ledger, engine Engine, opts Options) *Trigger {
	t.Helper()
	if opts.Metrics == nil {
		opts.Metrics = wire.NewOpsMetrics("schedd")
	}
	return New(appStore, depStore, ledger, engine, opts)
}

// TestTickPerDeployment_MaxOfAppAndDeploymentFloor pins issue #557
// AC #1 on the per-deployment axis: app floor=1, deployment floor=3
// → the per-deployment sweep must admit three instances, with the
// per-app call taking a backseat. The effective floor is the max.
func TestTickPerDeployment_MaxOfAppAndDeploymentFloor(t *testing.T) {
	appStore := &fakeStore{apps: []state.App{floorApp("app1", api.PlanHobby, 1)}}
	ledger := &fakeLedger{
		conc:     map[string]int{"app1": 0},
		depConc:  map[string]int{"app1\x00d1": 0},
		headroom: 47_600,
	}
	engine := &fakeEngine{}
	resolver := &fakePlanResolver{plans: map[string]api.Plan{"acct1": api.PlanHobby}}
	auditor := &fakeAuditor{}
	depStore := &fakeDeploymentStore{
		deps: []state.Deployment{floorDeployment("d1", "app1", 3)},
		apps: map[string]state.App{"app1": floorApp("app1", api.PlanHobby, 1)},
	}
	tr := withDeploymentStore(t, appStore, depStore, ledger, engine, Options{
		Auditor:      auditor,
		PlanResolver: resolver,
	})

	// 3 admits across 3 ticks (one per sweep, since the fake ledger
	// does not auto-increment like the production ledger does).
	for i := 1; i <= 3; i++ {
		if err := tr.Tick(context.Background()); err != nil {
			t.Fatalf("Tick %d: %v", i, err)
		}
	}
	if got, want := len(engine.calls), 3; got != want {
		t.Errorf("engine.calls = %d, want %d (effective=max(app=1, dep=3)=3 admits)", got, want)
	}
	for i, call := range engine.calls {
		if call != "app1|d1" {
			t.Errorf("engine.calls[%d] = %q, want app1|d1 (per-deployment key)", i, call)
		}
	}

	// 4th tick → ledger says conc=3, floor=3 → no admit.
	ledger.depConc["app1\x00d1"] = 3
	if err := tr.Tick(context.Background()); err != nil {
		t.Fatalf("Tick 4: %v", err)
	}
	if got, want := len(engine.calls), 3; got != want {
		t.Errorf("Tick 4 engine.calls = %d, want %d (floor met)", got, want)
	}

	// Auditor should have seen 3 dual-emits (6 emissions total).
	if got, want := len(auditor.emissions), 6; got != want {
		t.Errorf("auditor.emissions = %d, want %d (3 dual-emits)", got, want)
	}
}

// TestTickPerDeployment_InheritsAppFloor confirms the inverse
// direction: app floor=2, deployment floor=0 → the deployment
// inherits the parent app's floor (ADR-072 §Decision 2). Admit
// twice.
func TestTickPerDeployment_InheritsAppFloor(t *testing.T) {
	appStore := &fakeStore{apps: []state.App{floorApp("app1", api.PlanHobby, 2)}}
	ledger := &fakeLedger{
		conc:     map[string]int{"app1": 0},
		depConc:  map[string]int{"app1\x00d1": 0},
		headroom: 47_600,
	}
	engine := &fakeEngine{}
	resolver := &fakePlanResolver{plans: map[string]api.Plan{"acct1": api.PlanHobby}}
	depStore := &fakeDeploymentStore{
		deps: []state.Deployment{floorDeployment("d1", "app1", 0)},
		apps: map[string]state.App{"app1": floorApp("app1", api.PlanHobby, 2)},
	}
	tr := withDeploymentStore(t, appStore, depStore, ledger, engine, Options{PlanResolver: resolver})

	for i := 1; i <= 2; i++ {
		if err := tr.Tick(context.Background()); err != nil {
			t.Fatalf("Tick %d: %v", i, err)
		}
	}
	if got, want := len(engine.calls), 2; got != want {
		t.Errorf("engine.calls = %d, want %d (deployment floor=0 inherits app floor=2)", got, want)
	}
	for i, call := range engine.calls {
		if call != "app1|d1" {
			t.Errorf("engine.calls[%d] = %q, want app1|d1", i, call)
		}
	}
}

// TestTickPerDeployment_DualEmitsBothAuditKinds pins ADR-072
// §Decision 6: one admit produces BOTH
// `instances.warmed_min_instances` and `floor.wake` events, both
// carrying the deployment_id. The data shape matches what
// downstream consumers (apid dashboard, billing reconciliation)
// expect.
func TestTickPerDeployment_DualEmitsBothAuditKinds(t *testing.T) {
	appStore := &fakeStore{apps: []state.App{floorApp("app1", api.PlanHobby, 1)}}
	ledger := &fakeLedger{
		conc:     map[string]int{"app1": 0},
		depConc:  map[string]int{"app1\x00d1": 0},
		headroom: 47_600,
	}
	engine := &fakeEngine{results: map[string]AdmitResult{"app1|d1": {InstanceID: "iid-abc"}}}
	resolver := &fakePlanResolver{plans: map[string]api.Plan{"acct1": api.PlanHobby}}
	auditor := &fakeAuditor{}
	depStore := &fakeDeploymentStore{
		deps: []state.Deployment{floorDeployment("d1", "app1", 1)},
		apps: map[string]state.App{"app1": floorApp("app1", api.PlanHobby, 1)},
	}
	tr := withDeploymentStore(t, appStore, depStore, ledger, engine, Options{
		Auditor:      auditor,
		PlanResolver: resolver,
	})

	if err := tr.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	if got, want := len(auditor.emissions), 2; got != want {
		t.Fatalf("auditor.emissions = %d, want %d (dual-emit)", got, want)
	}

	// Order: instances.warmed_min_instances first, floor.wake second
	// (the trigger code emits them in that order — keep the test
	// pinned to the call site, not a permutation).
	wantKinds := []string{"instances.warmed_min_instances", "floor.wake"}
	for i, em := range auditor.emissions {
		if em.kind != wantKinds[i] {
			t.Errorf("auditor.emissions[%d].kind = %q, want %q", i, em.kind, wantKinds[i])
		}
		if em.data["deployment_id"] != "d1" {
			t.Errorf("auditor.emissions[%d] deployment_id = %v, want d1", i, em.data["deployment_id"])
		}
		if em.data["app_id"] != "app1" {
			t.Errorf("auditor.emissions[%d] app_id = %v, want app1", i, em.data["app_id"])
		}
		if em.data["wake_id"] != "iid-abc" {
			t.Errorf("auditor.emissions[%d] wake_id = %v, want iid-abc", i, em.data["wake_id"])
		}
		if em.data["floor"] != 1 {
			t.Errorf("auditor.emissions[%d] floor = %v, want 1", i, em.data["floor"])
		}
	}
}

// TestTickPerDeployment_FreePlanDisabled confirms the plan gate
// fires on the per-deployment axis too: a Free-plan app with a
// deployment floor set MUST be silently dropped, mirroring the
// per-app TestTick_FreePlanDisabled behaviour.
func TestTickPerDeployment_FreePlanDisabled(t *testing.T) {
	appStore := &fakeStore{apps: []state.App{floorApp("app1", api.PlanFree, 0)}}
	ledger := &fakeLedger{
		conc:     map[string]int{"app1": 0},
		depConc:  map[string]int{"app1\x00d1": 0},
		headroom: 47_600,
	}
	engine := &fakeEngine{}
	resolver := &fakePlanResolver{plans: map[string]api.Plan{"acct1": api.PlanFree}}
	depStore := &fakeDeploymentStore{
		// Customer tried to set dep floor=2, but Free plan disables it.
		deps: []state.Deployment{floorDeployment("d1", "app1", 2)},
		apps: map[string]state.App{"app1": floorApp("app1", api.PlanFree, 0)},
	}
	tr := withDeploymentStore(t, appStore, depStore, ledger, engine, Options{PlanResolver: resolver})

	if err := tr.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if len(engine.calls) != 0 {
		t.Errorf("engine.calls = %v, want [] (Free plan disables per-deployment floor too)", engine.calls)
	}
}

func TestTickPerDeployment_NonLiveDeploymentDisabled(t *testing.T) {
	statuses := []state.DeploymentStatus{
		state.DeployPending,
		state.DeployBuilding,
		state.DeployImaging,
		state.DeploySnapshotting,
		state.DeployFailed,
		state.DeploySuperseded,
		state.DeployCancelled,
	}
	for _, status := range statuses {
		t.Run(string(status), func(t *testing.T) {
			app := floorApp("app1", api.PlanHobby, 1)
			engine := &fakeEngine{}
			dep := floorDeployment("d1", app.ID, 1)
			dep.Status = status
			tr := withDeploymentStore(t,
				&fakeStore{apps: []state.App{app}},
				&fakeDeploymentStore{deps: []state.Deployment{dep}, apps: map[string]state.App{app.ID: app}},
				&fakeLedger{depConc: map[string]int{app.ID + "\x00" + dep.ID: 0}, headroom: 47_600},
				engine,
				Options{PlanResolver: &fakePlanResolver{plans: map[string]api.Plan{app.AccountID: api.PlanHobby}}},
			)

			if err := tr.Tick(context.Background()); err != nil {
				t.Fatalf("Tick: %v", err)
			}
			if len(engine.calls) != 0 {
				t.Fatalf("engine.calls = %v, want none for deployment status %q", engine.calls, status)
			}
		})
	}
}

// TestTickPerDeployment_AppByIDErrorObservesError exercises the
// defensive branch at tickPerDeployment:435-439. If the parent app
// lookup fails, the trigger MUST observe the OutcomeError metric
// (so the operator alarm fires) and continue to the next
// deployment rather than aborting the whole sweep.
func TestTickPerDeployment_AppByIDErrorObservesError(t *testing.T) {
	appStore := &fakeStore{apps: []state.App{floorApp("app1", api.PlanHobby, 1)}}
	ledger := &fakeLedger{conc: map[string]int{"app1": 0}, headroom: 47_600}
	engine := &fakeEngine{}
	resolver := &fakePlanResolver{plans: map[string]api.Plan{"acct1": api.PlanHobby}}
	depStore := &fakeDeploymentStore{
		deps: []state.Deployment{
			floorDeployment("d1", "app1", 1),
			floorDeployment("d2", "app2", 1),
		},
		// app1 lookup errors; app2 succeeds.
		appByIDErr: map[string]error{"app1": errors.New("store down")},
		apps:       map[string]state.App{"app2": floorApp("app2", api.PlanHobby, 1)},
	}
	tr := withDeploymentStore(t, appStore, depStore, ledger, engine, Options{PlanResolver: resolver})

	if err := tr.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	// app1 errored → no admit. app2 has conc=0 floor=1 → admit once.
	if got, want := len(engine.calls), 1; got != want {
		t.Errorf("engine.calls = %d, want %d (app1 errored, app2 admitted)", got, want)
	}
	if engine.calls[0] != "app2|d2" {
		t.Errorf("engine.calls[0] = %q, want app2|d2", engine.calls[0])
	}
}

// TestTickPerDeployment_FloorMetSkips pins the satisfied-floor
// shortcut on the per-deployment axis: when the ledger reports
// concurrency >= effective floor, the trigger emits
// OutcomeFloorMet and does NOT call AdmitInstanceForDeployment.
// This is the per-deployment equivalent of TestTick_AdmitsUpToFloor
// tick 3.
func TestTickPerDeployment_FloorMetSkips(t *testing.T) {
	appStore := &fakeStore{apps: []state.App{floorApp("app1", api.PlanHobby, 1)}}
	ledger := &fakeLedger{
		conc:     map[string]int{"app1": 3},
		depConc:  map[string]int{"app1\x00d1": 3},
		headroom: 47_600,
	}
	engine := &fakeEngine{}
	resolver := &fakePlanResolver{plans: map[string]api.Plan{"acct1": api.PlanHobby}}
	depStore := &fakeDeploymentStore{
		deps: []state.Deployment{floorDeployment("d1", "app1", 3)},
		apps: map[string]state.App{"app1": floorApp("app1", api.PlanHobby, 1)},
	}
	tr := withDeploymentStore(t, appStore, depStore, ledger, engine, Options{PlanResolver: resolver})

	if err := tr.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if len(engine.calls) != 0 {
		t.Errorf("engine.calls = %v, want [] (floor met at 3, no admit)", engine.calls)
	}
}

// TestTickPerDeployment_OwnerNodeIDRoutesToListDeploymentsByNodeID
// mirrors TestTick_OwnerNodeIDRoutesToListAppsByNodeID on the
// per-deployment axis. The trigger's WithOwnerNodeID must flip the
// deployment sweep from ListAllDeployments to
// ListDeploymentsByNodeID, passing the owner node id through
// unchanged.
func TestTickPerDeployment_OwnerNodeIDRoutesToListDeploymentsByNodeID(t *testing.T) {
	appStore := &fakeStore{apps: []state.App{floorApp("app1", api.PlanHobby, 2)}}
	ledger := &fakeLedger{
		conc:     map[string]int{"app1": 0},
		depConc:  map[string]int{"app1\x00d1": 0},
		headroom: 47_600,
	}
	engine := &fakeEngine{}
	resolver := &fakePlanResolver{plans: map[string]api.Plan{"acct1": api.PlanHobby}}
	depStore := &fakeDeploymentStore{
		deps: []state.Deployment{floorDeployment("d1", "app1", 2)},
		apps: map[string]state.App{"app1": floorApp("app1", api.PlanHobby, 2)},
	}
	tr := withDeploymentStore(t, appStore, depStore, ledger, engine, Options{PlanResolver: resolver})

	// Unsharded → ListAllDeployments.
	if err := tr.Tick(context.Background()); err != nil {
		t.Fatalf("Tick (unsharded): %v", err)
	}
	if !depStore.listAllCalls {
		t.Error("unsharded Tick did not call ListAllDeployments")
	}
	if depStore.listByIDCalls != 0 {
		t.Errorf("unsharded Tick called ListDeploymentsByNodeID %d times, want 0", depStore.listByIDCalls)
	}

	// Sharded → ListDeploymentsByNodeID with the owner node id.
	tr.WithOwnerNodeID("node-7")
	if err := tr.Tick(context.Background()); err != nil {
		t.Fatalf("Tick (sharded): %v", err)
	}
	if depStore.listByIDCalls != 1 {
		t.Errorf("sharded Tick called ListDeploymentsByNodeID %d times, want 1", depStore.listByIDCalls)
	}
	if depStore.lastNodeID != "node-7" {
		t.Errorf("lastNodeID = %q, want node-7 (owner node id forwarded unchanged)", depStore.lastNodeID)
	}
}

// TestEffectiveMaxConcurrency pins the legacy-app clamp: a pre-PR-A
// app whose MaxConcurrency is 0 falls back to the plan ceiling, so
// the trigger does not silently allow unlimited admits.
func TestEffectiveMaxConcurrency(t *testing.T) {
	cases := []struct {
		name string
		app  state.App
		plan api.Plan
		want int
	}{
		{"unset clamps to plan", state.App{MaxConcurrency: 0}, api.PlanHobby, 2},
		{"under plan kept", state.App{MaxConcurrency: 1}, api.PlanHobby, 1},
		{"over plan clamped", state.App{MaxConcurrency: 999}, api.PlanHobby, 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := effectiveMaxConcurrency(c.app, c.plan)
			if got != c.want {
				t.Errorf("effectiveMaxConcurrency = %d, want %d", got, c.want)
			}
		})
	}
}
