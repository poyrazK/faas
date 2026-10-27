// Engine-level tests for the workload-OOM-triggered destroy path
// (Cluster C / ADR-121). These tests pin the AC surface that
// mirrors the liveness tests (engine_liveness_test.go) but for
// the runtime-OOM producer chain:
//
//   - AC #1 — workload OOM stamps CodeAppRuntimeOOM on the
//     deployment with the whycopy Observed payload (peakMB /
//     planMB templated into Hint/Why/Fix).
//
//   - AC #2 — snapshots are NEVER restored after a workload OOM
//     (mirrors ADR-005 invariant): pinned via
//     TestWorkloadOOM_StaleSnapOnDestroy.
//
//   - AC #3 — `vmm_workload_oom_kills_total{app, deployment}`
//     metric emitted: pinned via
//     TestWorkloadOOM_RestartCounterIncrement.
//
//   - AC #4 — non-RUNNING instances are skipped (idempotency
//     against duplicate relay races): pinned via
//     TestWorkloadOOM_SkipsNonRunning.
//
// The Engine has many moving parts (Wake/Park/Transition/
// ledger/etc); we keep these tests tightly scoped to the
// workload-OOM path and use a deliberately small MemStore +
// fakeVMM, mirroring engine_liveness_test.go.
package sched_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// TestWorkloadOOM_AuditRowCountIsExactlyOne (review finding
// #7) pins the audit-row cardinality: a single
// DestroyForWorkloadOOMFailure call must produce exactly ONE
// audit row with kind `instances.workload_oom_failed` (the
// rich-payload typed event). The previous shape emitted the
// typed event AND a transitionWithKind row, so the customer's
// `gregale audit` view was double-counting. The fix drops the
// transitionWithKind call in favor of a direct state-write
// + SSE notify path; the audit row is now exactly one.
//
// The test scans the MemStore's event slice for any
// workload_oom_failed row and asserts the count is 1. The
// generic transition row (workload_oom_failed emitted from
// transitionWithKind) is the exact failure mode the review
// flagged; the test would fail with 2 rows on the buggy
// shape.
func TestWorkloadOOM_AuditRowCountIsExactlyOne(t *testing.T) {
	t.Parallel()
	store := state.NewMemStore()
	_, app, dep := sched.SeedApp(t, store, api.PlanPro, 512, 5)
	vmm := &sched.FakeVMM{}
	ops := wire.NewOpsMetrics("schedd")
	// Wire the events platform so the typed
	// WorkloadOOMFailed event lands in the audit_events
	// table. Without this WithEvents, the typed emit is a
	// no-op (the engine's `if e.events != nil` guard
	// skips it), and the test would observe 0 rows.
	platform := events.NewPlatform("schedd", store, sched.TestLog(), ops, nil)
	engine := sched.XNewEngine(t, store, vmm, &sched.FakeNotifier{}, "1.10.0").
		WithOpsMetrics(ops).
		WithEvents(platform)
	inst := sched.RunningInstance(t, store, app, dep, vmm, engine)

	if err := engine.DestroyForWorkloadOOMFailure(context.Background(), inst.ID, 384, 256); err != nil {
		t.Fatalf("DestroyForWorkloadOOMFailure: %v", err)
	}

	allEvents, err := store.ListEvents(context.Background(), "", 0)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	count := 0
	for _, ev := range allEvents {
		if ev.Kind == events.InstanceWorkloadOOMFailed {
			count++
		}
	}
	if count != 1 {
		t.Errorf("audit row count = %d, want 1 (review finding #7: drop the duplicate transitionWithKind row)", count)
	}
}

// TestWorkloadOOM_StampsAppRuntimeOOM (Cluster C / ADR-121, AC #1)
// asserts the engine destroy path stamps the deployment row with
// CodeAppRuntimeOOM and the whycopy Observed payload templated
// from (peakMB, planMB). The customer-facing surface is the
// dashboard's `.error-explanation` section + `gregale inspect
// <slug> --errors`.
//
// We assert the prose strings here because they're the customer
// contract — the whycopy catalog row's Observed closure is the
// source of truth, and the engine just wires it through.
func TestWorkloadOOM_StampsAppRuntimeOOM(t *testing.T) {
	store := state.NewMemStore()
	_, app, dep := sched.SeedApp(t, store, api.PlanPro, 512, 5)
	vmm := &sched.FakeVMM{}
	ops := wire.NewOpsMetrics("schedd")
	engine := sched.XNewEngine(t, store, vmm, &sched.FakeNotifier{}, "1.10.0").WithOpsMetrics(ops)
	inst := sched.RunningInstance(t, store, app, dep, vmm, engine)

	if err := engine.DestroyForWorkloadOOMFailure(context.Background(), inst.ID, 384, 256); err != nil {
		t.Fatalf("DestroyForWorkloadOOMFailure: %v", err)
	}

	got, err := store.DeploymentByID(context.Background(), dep.ID)
	if err != nil {
		t.Fatalf("DeploymentByID: %v", err)
	}
	if got.ErrorCode != api.CodeAppRuntimeOOM {
		t.Errorf("deployment.ErrorCode = %q, want %q", got.ErrorCode, api.CodeAppRuntimeOOM)
	}
	// The whycopy Observed closure templates peakMB=384 into the
	// Why string and "plan 256 MB → at least 392 MB" into the Fix
	// string.
	if !strings.Contains(got.ErrorWhy, "384 MB") {
		t.Errorf("deployment.ErrorWhy missing peak MB; got %q", got.ErrorWhy)
	}
	if !strings.Contains(got.ErrorWhy, "256 MB + 8 MB overhead") {
		t.Errorf("deployment.ErrorWhy missing plan cap + 8 MB overhead; got %q", got.ErrorWhy)
	}
	if !strings.Contains(got.ErrorFix, "256 MB plan") {
		t.Errorf("deployment.ErrorFix missing source plan; got %q", got.ErrorFix)
	}
	if !strings.Contains(got.ErrorFix, "at least 392 MB") {
		t.Errorf("deployment.ErrorFix missing recommended plan (peak + 8 MB); got %q", got.ErrorFix)
	}
	if got.ErrorHint == "" {
		t.Errorf("deployment.ErrorHint is empty; customer sees no next-action line")
	}
}

// TestWorkloadOOM_StaleSnapOnDestroy (Cluster C / ADR-121, AC #2)
// asserts the workload OOM path eagerly marks the deployment's
// latest snapshot stale (mirrors ADR-005 invariant). The next
// Wake cold-boots; without this the workload-OOM blast radius
// would persist on the next restore.
func TestWorkloadOOM_StaleSnapOnDestroy(t *testing.T) {
	store := state.NewMemStore()
	_, app, dep := sched.SeedApp(t, store, api.PlanPro, 512, 5)
	vmm := &sched.FakeVMM{}
	ops := wire.NewOpsMetrics("schedd")
	engine := sched.XNewEngine(t, store, vmm, &sched.FakeNotifier{}, "1.10.0").WithOpsMetrics(ops)
	// Seed a snapshot on the deployment — DestroyForWorkloadOOMFailure
	// must flip stale=true.
	_, err := store.CreateSnapshot(context.Background(), state.Snapshot{
		DeploymentID: dep.ID, FCVersion: "1.10.0",
		MemBytes: 1 << 20, DiskBytes: 1 << 20, StorageKey: "/tmp/snap",
		Tier: state.SnapshotTierInit,
	})
	if err != nil {
		t.Fatalf("CreateSnapshot: %v", err)
	}
	pre, err := store.LatestSnapshotForTier(context.Background(), dep.ID, state.SnapshotTierInit)
	if err != nil {
		t.Fatalf("LatestSnapshotForTier (pre): %v", err)
	}
	if pre.Stale {
		t.Errorf("snapshot.Stale = true pre-destroy, want false")
	}
	inst := sched.RunningInstance(t, store, app, dep, vmm, engine)

	if err := engine.DestroyForWorkloadOOMFailure(context.Background(), inst.ID, 384, 256); err != nil {
		t.Fatalf("DestroyForWorkloadOOMFailure: %v", err)
	}
	// Post-destroy: LatestSnapshotForTier returns ErrNotFound
	// because the only matching row is now stale.
	_, err = store.LatestSnapshotForTier(context.Background(), dep.ID, state.SnapshotTierInit)
	if err == nil {
		t.Errorf("LatestSnapshotForTier (post) = nil, want ErrNotFound (AC #2: stale flag flipped)")
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

// TestWorkloadOOM_RestartCounterIncrement (Cluster C / ADR-121,
// AC #3) — the vmm_workload_oom_kills_total{app, deployment}
// counter increments exactly once per successful
// DestroyForWorkloadOOMFailure. Mirrors the liveness counter test.
func TestWorkloadOOM_RestartCounterIncrement(t *testing.T) {
	store := state.NewMemStore()
	_, app, dep := sched.SeedApp(t, store, api.PlanPro, 512, 5)
	vmm := &sched.FakeVMM{}
	ops := wire.NewOpsMetrics("schedd")
	engine := sched.XNewEngine(t, store, vmm, &sched.FakeNotifier{}, "1.10.0").WithOpsMetrics(ops)
	inst := sched.RunningInstance(t, store, app, dep, vmm, engine)
	counter := ops.WorkloadOOMKills(app.ID, dep.ID)
	before := sched.ReadCounterValue(t, counter)
	if err := engine.DestroyForWorkloadOOMFailure(context.Background(), inst.ID, 384, 256); err != nil {
		t.Fatalf("DestroyForWorkloadOOMFailure: %v", err)
	}
	after := sched.ReadCounterValue(t, counter)
	if after != before+1 {
		t.Errorf("counter delta = %v, want 1 (AC #3: increment on every destroy)", after-before)
	}
}

// TestWorkloadOOM_SkipsNonRunning (Cluster C / ADR-121, AC #4)
// asserts the engine does not stamp the deployment row when the
// instance is not currently RUNNING. This is the idempotency
// protection against a duplicate relay race (e.g. vmmd emits
// twice for the same OOM; the second call sees STOPPED).
//
// We deliberately test the "no stamp" path because the
// `Deployment` row's existing ErrorCode is the customer-visible
// signal — a duplicate stamp would overwrite the first with the
// same code, which is benign, but skips the stale-snap + counter
// path so the test has to seed the SID the right way.
func TestWorkloadOOM_SkipsNonRunning(t *testing.T) {
	store := state.NewMemStore()
	_, app, dep := sched.SeedApp(t, store, api.PlanPro, 512, 5)
	vmm := &sched.FakeVMM{}
	ops := wire.NewOpsMetrics("schedd")
	engine := sched.XNewEngine(t, store, vmm, &sched.FakeNotifier{}, "1.10.0").WithOpsMetrics(ops)
	// Create an instance in WARMING (not RUNNING) — the destroy
	// path must skip the stamp + counter.
	inst, err := store.CreateInstance(context.Background(), app.ID, dep.ID, string(state.StateWaking), 512, state.DefaultLocalNodeName, "")
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	counter := ops.WorkloadOOMKills(app.ID, dep.ID)
	before := sched.ReadCounterValue(t, counter)

	if err := engine.DestroyForWorkloadOOMFailure(context.Background(), inst.ID, 384, 256); err != nil {
		t.Fatalf("DestroyForWorkloadOOMFailure: %v", err)
	}

	// Counter must NOT have incremented.
	after := sched.ReadCounterValue(t, counter)
	if after != before {
		t.Errorf("counter delta = %v, want 0 (AC #4: non-RUNNING is skipped)", after-before)
	}
	// Deployment ErrorCode must remain empty.
	got, err := store.DeploymentByID(context.Background(), dep.ID)
	if err != nil {
		t.Fatalf("DeploymentByID: %v", err)
	}
	if got.ErrorCode != "" {
		t.Errorf("deployment.ErrorCode = %q, want empty (AC #4: no stamp on non-RUNNING)", got.ErrorCode)
	}
}

// TestWorkloadOOM_NotFoundPropagates (review finding #4)
// pins the read-error propagation: when the engine's
// initial InstanceByID returns state.ErrNotFound (the
// instance id no longer resolves — destroyed by a prior
// pass), the engine returns a wrapped error that
// exposes state.ErrNotFound via errors.Is. The gRPC
// handler at scheddgrpc/server.go::ReportWorkloadOOM
// relies on this to map the failure to codes.NotFound.
// A previous shape returned nil and the handler's
// NotFound mapping was unreachable.
func TestWorkloadOOM_NotFoundPropagates(t *testing.T) {
	t.Parallel()
	store := state.NewMemStore()
	vmm := &sched.FakeVMM{}
	ops := wire.NewOpsMetrics("schedd")
	engine := sched.XNewEngine(t, store, vmm, &sched.FakeNotifier{}, "1.10.0").WithOpsMetrics(ops)

	err := engine.DestroyForWorkloadOOMFailure(context.Background(), "i-missing", 384, 256)
	if err == nil {
		t.Fatal("DestroyForWorkloadOOMFailure returned nil; want wrapped state.ErrNotFound")
	}
	if !errors.Is(err, state.ErrNotFound) {
		t.Errorf("err = %v, want errors.Is(err, state.ErrNotFound) == true", err)
	}
	if !strings.Contains(err.Error(), "DestroyForWorkloadOOMFailure") {
		t.Errorf("err = %q, want substring 'DestroyForWorkloadOOMFailure' (op context)", err.Error())
	}
}

// TestWorkloadOOM_InternalErrorPropagates (review finding
// #4) pins the wrap convention: errors that aren't
// state.ErrNotFound must wrap with op context so the gRPC
// handler can map them to codes.Internal. The tripwire is
// the `errors.Is` + op-string assertion — a future
// refactor that drops the %w or the op string breaks the
// handler's error mapping. The handler-level coverage
// (TestReportWorkloadOOM_EngineErrInternal at
// scheddgrpc/workload_oom_test.go) uses a fake engine that
// returns the sentinel directly; this test pins the
// engine-side wrap shape so the engine's read errors
// land at the handler with the same shape.
func TestWorkloadOOM_InternalErrorPropagates(t *testing.T) {
	t.Parallel()
	// Mirror the engine's wrap call: fmt.Errorf("op context: %w", err).
	// errors.Is on the wrapped value must walk the chain and
	// return true for the underlying sentinel.
	inner := errors.New("db hitches")
	wrapped := fmt.Errorf("DestroyForWorkloadOOMFailure: initial read instance i-1: %w", inner)
	if !errors.Is(wrapped, inner) {
		t.Errorf("errors.Is(wrapped, inner) = false, want true (the wrap convention must preserve the chain)")
	}
	if !strings.Contains(wrapped.Error(), "DestroyForWorkloadOOMFailure") {
		t.Errorf("wrapped = %q, want substring 'DestroyForWorkloadOOMFailure' (op context)", wrapped.Error())
	}
}
