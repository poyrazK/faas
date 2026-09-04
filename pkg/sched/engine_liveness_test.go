// Engine-level tests for the liveness-probe restart path
// (issue #554 / ADR-078). These tests pin the AC surface:
//
//   - AC #1 — wedged app replaced within the budget: not pinned
//     here (covered by metal test); we pin the lighter invariants.
//
//   - AC #4 — snapshots are NEVER restored after a liveness
//     failure: pinned via TestLiveness_StaleSnapOnDestroy
//     (destroy-side, snapshot goes stale) + the wake-side
//     counterpart TestLiveness_StaleSnapAndColdBootOnlyAfterDestroy
//     (next wake MUST take the cold-boot path).
//
//   - AC #6 — `liveness_restarts_total{app, deployment}` metric
//     emitted: pinned via TestLiveness_RestartCounterIncrement.
//
// The Engine has many moving parts (Wake/Park/Transition/
// ledger/etc); we keep these tests tightly scoped to the liveness
// path and use a deliberately small MemStore + fakeVMM.
package sched

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// runningInstance creates a RUNNING instance row in the store +
// admits a ledger reservation. Mirrors the watchdog test shape
// (engine_test.go:428 seedApp + WatchdogSweepKillsStuck
// admit/instance creation).
func runningInstance(t *testing.T, store state.Store, app state.App, dep state.Deployment, vmm *fakeVMM, engine *Engine) state.Instance {
	t.Helper()
	inst, err := store.CreateInstance(context.Background(), app.ID, dep.ID, string(state.StateRunning), 512, state.DefaultLocalNodeName, "")
	if err != nil {
		t.Fatalf("CreateInstance(RUNNING): %v", err)
	}
	engine.Ledger().Admit(Request{
		Instance: inst.ID, AppID: app.ID, Plan: api.PlanPro,
		RAMMB: 512, VCPU: 2, MaxConcurrency: 5,
	})
	return inst
}

// TestLiveness_StaleSnapOnDestroy (AC #4) — after
// DestroyForLivenessFailure fires, the deployment's latest
// snapshot must be marked stale so the next Wake cold-boots
// (ADR-005). Without this the wedged snapshot would be restored
// and the customer outage persists.
func TestLiveness_StaleSnapOnDestroy(t *testing.T) {
	store := state.NewMemStore()
	_, app, dep := seedApp(t, store, api.PlanPro, 512, 5)
	vmm := &fakeVMM{}
	ops := wire.NewOpsMetrics("schedd")
	engine := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0").WithOpsMetrics(ops)
	// Seed a snapshot on the deployment — DestroyForLivenessFailure
	// must flip stale=true.
	_, err := store.CreateSnapshot(context.Background(), state.Snapshot{
		DeploymentID: dep.ID, FCVersion: "1.10.0",
		MemBytes: 1 << 20, DiskBytes: 1 << 20, StorageKey: "/tmp/snap",
		Tier: state.SnapshotTierInit,
	})
	if err != nil {
		t.Fatalf("CreateSnapshot: %v", err)
	}
	// Pre-destroy: the snapshot is non-stale and LatestSnapshotForTier
	// finds it.
	pre, err := store.LatestSnapshotForTier(context.Background(), dep.ID, state.SnapshotTierInit)
	if err != nil {
		t.Fatalf("LatestSnapshotForTier (pre): %v", err)
	}
	if pre.Stale {
		t.Errorf("snapshot.Stale = true pre-destroy, want false")
	}
	inst := runningInstance(t, store, app, dep, vmm, engine)

	if err := engine.DestroyForLivenessFailure(context.Background(), inst.ID, "liveness_n_consecutive"); err != nil {
		t.Fatalf("DestroyForLivenessFailure: %v", err)
	}
	// Post-destroy: LatestSnapshotForTier returns ErrNotFound
	// because the only matching row is now stale (= "no
	// non-stale snapshot for this tier", which is what
	// usableSnapshotForWake consumes to force a cold boot).
	_, err = store.LatestSnapshotForTier(context.Background(), dep.ID, state.SnapshotTierInit)
	if err == nil {
		t.Errorf("LatestSnapshotForTier (post) = nil, want ErrNotFound (AC #4: stale flag flipped)")
	}
	// Instance row must be STOPPED — the destroy succeeded.
	final, err := store.InstanceByID(context.Background(), inst.ID)
	if err != nil {
		t.Fatalf("InstanceByID: %v", err)
	}
	if state.State(final.State) != state.StateStopped {
		t.Errorf("instance.State = %q, want %q", final.State, state.StateStopped)
	}
}

// TestLiveness_RestartCounterIncrement (AC #6) — the
// liveness_restarts_total{app, deployment} counter increments
// exactly once per successful DestroyForLivenessFailure.
func TestLiveness_RestartCounterIncrement(t *testing.T) {
	store := state.NewMemStore()
	_, app, dep := seedApp(t, store, api.PlanPro, 512, 5)
	vmm := &fakeVMM{}
	ops := wire.NewOpsMetrics("schedd")
	engine := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0").WithOpsMetrics(ops)
	inst := runningInstance(t, store, app, dep, vmm, engine)
	counter := ops.LivenessRestarts(app.ID, dep.ID)
	before := readCounterValue(t, counter)
	if err := engine.DestroyForLivenessFailure(context.Background(), inst.ID, "liveness_n_consecutive"); err != nil {
		t.Fatalf("DestroyForLivenessFailure: %v", err)
	}
	after := readCounterValue(t, counter)
	if after != before+1 {
		t.Errorf("counter delta = %v, want 1 (AC #6: increment on every destroy)", after-before)
	}
}

// TestLiveness_InfrastructureRecoveryDoesNotParkApp pins issue #1267's
// failure-class boundary. Infrastructure-correlated replacements are valid
// recovery actions, but three of them must not flip the parent app to
// evicted_cold or advance the durable guest-failure counter.
func TestLiveness_InfrastructureRecoveryDoesNotParkApp(t *testing.T) {
	store := state.NewMemStore()
	_, app, dep := seedApp(t, store, api.PlanPro, 512, 5)
	vmm := &fakeVMM{}
	window := NewLivenessWindow(5*time.Minute, 3)
	engine := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0").WithLivenessWindow(window)

	for i := 0; i < 3; i++ {
		inst := runningInstance(t, store, app, dep, vmm, engine)
		if err := engine.DestroyForLivenessFailure(context.Background(), inst.ID, fcvm.LivenessReasonInfrastructure); err != nil {
			t.Fatalf("infrastructure destroy %d: %v", i+1, err)
		}
	}
	if got := window.recent(dep.ID, time.Now()); got != 0 {
		t.Fatalf("infrastructure restart window count=%d, want 0", got)
	}
	finalApp, err := store.AppByID(context.Background(), app.ID)
	if err != nil {
		t.Fatalf("AppByID: %v", err)
	}
	if finalApp.Status == state.AppEvictedCold {
		t.Fatalf("app status=%q, want active after infrastructure-only recoveries", finalApp.Status)
	}
	finalDep, err := store.DeploymentByID(context.Background(), dep.ID)
	if err != nil {
		t.Fatalf("DeploymentByID: %v", err)
	}
	if finalDep.LivenessRestartCount != 0 {
		t.Fatalf("durable restart count=%d, want 0 for infrastructure-only recoveries", finalDep.LivenessRestartCount)
	}
}

// TestLiveness_DestroyTimeoutDoesNotAdvanceBudget pins the scheduler-side
// half of issue #1267. A failed destroy is a control-plane/node observation,
// not a confirmed restart, so it cannot consume the permanent-eviction
// budget even though the state transition remains best-effort and idempotent.
func TestLiveness_DestroyTimeoutDoesNotAdvanceBudget(t *testing.T) {
	store := state.NewMemStore()
	_, app, dep := seedApp(t, store, api.PlanPro, 512, 5)
	vmm := &fakeVMM{destroyErr: context.DeadlineExceeded}
	window := NewLivenessWindow(5*time.Minute, 1)
	engine := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0").WithLivenessWindow(window)
	inst := runningInstance(t, store, app, dep, vmm, engine)

	if err := engine.DestroyForLivenessFailure(context.Background(), inst.ID, "liveness_timeout"); err != nil {
		t.Fatalf("DestroyForLivenessFailure: %v", err)
	}
	if got := window.recent(dep.ID, time.Now()); got != 0 {
		t.Fatalf("restart window count=%d after failed destroy, want 0", got)
	}
	finalDep, err := store.DeploymentByID(context.Background(), dep.ID)
	if err != nil {
		t.Fatalf("DeploymentByID: %v", err)
	}
	if finalDep.LivenessRestartCount != 0 {
		t.Fatalf("durable restart count=%d after failed destroy, want 0", finalDep.LivenessRestartCount)
	}
}

// TestLiveness_NilReceiverSafe pins that tests/cmd that opt out
// of the liveness window (no WithLivenessWindow call) don't
// crash DestroyForLivenessFailure. The must-not-panic branch
// is the only invariant here.
func TestLiveness_NilReceiverSafe(t *testing.T) {
	store := state.NewMemStore()
	_, app, dep := seedApp(t, store, api.PlanPro, 512, 5)
	vmm := &fakeVMM{}
	ops := wire.NewOpsMetrics("schedd")
	// Engine constructed WITHOUT WithLivenessWindow — DestroyForLivenessFailure
	// must skip the RecordRestart step.
	engine := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0").WithOpsMetrics(ops)
	inst := runningInstance(t, store, app, dep, vmm, engine)
	if err := engine.DestroyForLivenessFailure(context.Background(), inst.ID, "liveness_n_consecutive"); err != nil {
		t.Fatalf("DestroyForLivenessFailure: %v (nil-window must be safe)", err)
	}
}

// TestLiveness_ParkDeployment_RejectsStrayReason pins the
// closed-set guard added to Engine.ParkDeployment. A stray
// reason (one not in {liveness_exhausted, lifecycle_park,
// admin_park}) must surface as a hard error — the
// deployments.parked_reason CHECK constraint would reject it
// at the SQL layer, and the silent warn-log in
// SetDeploymentParked's caller path would mask the bug. The
// guard moves the failure to the API boundary so a future
// stray-reason caller is caught in dev, not in prod.
//
// This is the dev-time contract for the closed-set
// vocabulary; the migration 00157 test pins the schema-layer
// CHECK shape.
func TestLiveness_ParkDeployment_RejectsStrayReason(t *testing.T) {
	store := state.NewMemStore()
	_, _, dep := seedApp(t, store, api.PlanPro, 512, 5)
	engine := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0")

	err := engine.ParkDeployment(context.Background(), dep.ID, "totally_not_a_park_reason")
	if err == nil {
		t.Fatal("ParkDeployment: stray reason returned nil, want error (closed-set guard)")
	}
	if !strings.Contains(err.Error(), "invalid reason") {
		t.Errorf("err = %q, want substring %q", err.Error(), "invalid reason")
	}

	// Verify the parent app was NOT flipped to evicted_cold
	// (the guard fires before the UpdateApp call).
	app, err := store.AppByID(context.Background(), dep.AppID)
	if err != nil {
		t.Fatalf("AppByID: %v", err)
	}
	if string(app.Status) == "evicted_cold" {
		t.Errorf("app.Status = %q after stray-reason park; guard must short-circuit before UpdateApp", app.Status)
	}

	// Verify the per-deployment parked_reason column stays
	// NULL (SetDeploymentParked is called AFTER the guard
	// would have rejected; the guard fires first).
	post, err := store.DeploymentByID(context.Background(), dep.ID)
	if err != nil {
		t.Fatalf("DeploymentByID: %v", err)
	}
	if post.ParkedReason != "" {
		t.Errorf("deployment.ParkedReason = %q after stray-reason park, want empty", post.ParkedReason)
	}
}

// TestLiveness_3In5MinParksDeploymentAndPersistsReason (AC #3) —
// three destroys in the window parks the parent app AND stamps
// the per-deployment parked_reason + parked_at columns (issue
// #554 follow-up / migration 00157). Uses the live LivenessWindow
// + a real memstore UpdateApp call.
func TestLiveness_3In5MinParksDeploymentAndPersistsReason(t *testing.T) {
	store := state.NewMemStore()
	_, app, dep := seedApp(t, store, api.PlanPro, 512, 5)
	vmm := &fakeVMM{}
	ops := wire.NewOpsMetrics("schedd")
	window := NewLivenessWindow(5*time.Minute, 3)
	engine := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0").
		WithOpsMetrics(ops).
		WithLivenessWindow(window)

	// Three destroys → 3 RecordRestarts → ParkDeployment fires.
	for i := 0; i < 3; i++ {
		inst := runningInstance(t, store, app, dep, vmm, engine)
		if err := engine.DestroyForLivenessFailure(context.Background(), inst.ID, "liveness_n_consecutive"); err != nil {
			t.Fatalf("destroy %d: %v", i+1, err)
		}
	}
	// ParkDeployment flips apps.status='evicted_cold'.
	final, err := store.AppByID(context.Background(), app.ID)
	if err != nil {
		t.Fatalf("AppByID: %v", err)
	}
	if string(final.Status) != "evicted_cold" {
		t.Errorf("app.Status = %q, want %q (AC #3: park after 3 restarts in window)", final.Status, "evicted_cold")
	}

	// AC #3 follow-up: the per-deployment parked_reason +
	// parked_at columns land so the apid
	// GET /v1/apps/{slug}.parked_deployment reference can
	// render. ParkDeployment's call site passes the closed-set
	// reason "liveness_exhausted" — assert that, not the
	// liveness-window reason ("liveness_n_consecutive"), since
	// the column CHECK enforces the closed vocabulary.
	post, err := store.DeploymentByID(context.Background(), dep.ID)
	if err != nil {
		t.Fatalf("DeploymentByID: %v", err)
	}
	if post.ParkedReason != "liveness_exhausted" {
		t.Errorf("deployment.ParkedReason = %q, want %q (AC #3 wire: closed-set column)",
			post.ParkedReason, "liveness_exhausted")
	}
	if post.ParkedAt == nil {
		t.Errorf("deployment.ParkedAt = nil, want stamped timestamp (AC #3 wire)")
	}
}

// TestLiveness_RaceIgnoresAlreadyParked pins that two
// DestroyForLivenessFailure calls racing on the same instance
// don't double-park or double-count. The lockApp dance inside
// DestroyForLivenessFailure serialises by app id; the second
// call sees the post-destroy instance row, which is no longer
// in state RUNNING, and returns nil (no-op).
func TestLiveness_RaceIgnoresAlreadyParked(t *testing.T) {
	store := state.NewMemStore()
	_, app, dep := seedApp(t, store, api.PlanPro, 512, 5)
	vmm := &fakeVMM{}
	ops := wire.NewOpsMetrics("schedd")
	window := NewLivenessWindow(5*time.Minute, 3)
	engine := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0").
		WithOpsMetrics(ops).
		WithLivenessWindow(window)

	// Two parallel destroys on the SAME instance. The
	// app-lock dance means exactly one wins; the loser sees a
	// non-RUNNING state and returns nil.
	inst := runningInstance(t, store, app, dep, vmm, engine)
	errCh := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			errCh <- engine.DestroyForLivenessFailure(context.Background(), inst.ID, "liveness_n_consecutive")
		}()
	}
	for i := 0; i < 2; i++ {
		if err := <-errCh; err != nil {
			t.Errorf("parallel destroy returned %v, want nil (state machine serialises)", err)
		}
	}
	// Counter must increment at most once.
	counter := ops.LivenessRestarts(app.ID, dep.ID)
	got := readCounterValue(t, counter)
	if got > 1 {
		t.Errorf("counter incremented %v times on parallel destroy, want <= 1", got)
	}
	// The window ring has exactly one entry.
	if recent := window.recent(dep.ID, time.Now()); recent > 1 {
		t.Errorf("window.recent = %d, want <= 1", recent)
	}
}

// readCounterValue uses testutil.ToFloat64 to expose the current
// counter value — same pattern as watchdog_test.go.
func readCounterValue(t *testing.T, c prometheus.Counter) float64 {
	t.Helper()
	return testutil.ToFloat64(c)
}

// TestLiveness_StaleSnapAndColdBootOnlyAfterDestroy (AC #4 wake-
// side pin) — after DestroyForLivenessFailure marks the
// deployment's snapshots stale, the next usableSnapshotForWake
// call must return haveSnap=false so the wake path picks
// WakeColdBoot, not WakeRestore. Without this, a wedged snapshot
// would be restored on the next wake and the customer outage
// persists (ADR-005: "snapshot is cache, not truth").
//
// This is the wake-side counterpart to TestLiveness_StaleSnapOnDestroy
// (which only pins the destroy-side flip). The combined pin
// guarantees: a wedged snapshot is NEVER restored — every
// post-liveness-failure wake must cold-boot.
//
// We pin via usableSnapshotForWake directly rather than driving
// the full Wake flow because the fakeVMM's forceColdFallback
// branch (engine_test.go:1489) is already covered in the AC #1
// envelope (the wake side would still pick the cold-boot path
// here because haveSnap=false). The structural invariant — "no
// usable snapshot post-destroy" — is the load-bearing contract.
func TestLiveness_StaleSnapAndColdBootOnlyAfterDestroy(t *testing.T) {
	store := state.NewMemStore()
	_, _, dep := seedApp(t, store, api.PlanPro, 512, 5)
	vmm := &fakeVMM{}
	engine := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")

	// Seed a non-stale init snapshot.
	snap, err := store.CreateSnapshot(context.Background(), state.Snapshot{
		DeploymentID: dep.ID, FCVersion: "1.10.0",
		MemBytes: 1 << 20, DiskBytes: 1 << 20, StorageKey: "/tmp/snap",
		Tier: state.SnapshotTierInit,
	})
	if err != nil {
		t.Fatalf("CreateSnapshot: %v", err)
	}

	// Pre-destroy: usableSnapshotForWake must find the snapshot
	// (haveSnap=true → would take WakeRestore).
	_, preOK, preTier := engine.usableSnapshotForWake(context.Background(), dep.ID, string(api.PlanPro))
	if !preOK {
		t.Fatalf("usableSnapshotForWake (pre) = no snap; AC #4 wake-side pin depends on a real pre-state snap")
	}
	if preTier == "cold_boot_fallback" {
		t.Errorf("usableSnapshotForWake (pre) tier = %q, want init (warm is the post-upgrade sticky path)", preTier)
	}

	// Mark the snapshot stale (mirrors what
	// DestroyForLivenessFailure does — see AC #4 destroy-side
	// pin in TestLiveness_StaleSnapOnDestroy above at
	// engine.go:3678).
	if err := store.MarkSnapshotStale(context.Background(), snap.ID); err != nil {
		t.Fatalf("MarkSnapshotStale: %v", err)
	}

	// Post-destroy: usableSnapshotForWake MUST return haveSnap=false
	// → wake flow takes the cold-boot path, never WakeRestore.
	_, postOK, postTier := engine.usableSnapshotForWake(context.Background(), dep.ID, string(api.PlanPro))
	if postOK {
		t.Errorf("usableSnapshotForWake (post) haveSnap = true, want false (AC #4: stale snapshot must NOT be restored on next wake)")
	}
	if postTier != "cold_boot_fallback" {
		t.Errorf("usableSnapshotForWake (post) tier = %q, want cold_boot_fallback", postTier)
	}
}
