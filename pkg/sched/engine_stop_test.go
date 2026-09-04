// engine_stop_test.go — portable (no KVM, no pgtest) tests for the
// M-2 / ADR-138 mode-aware Engine.StopInstance dispatch. The tests
// pin three behaviours:
//
//   1. mode='worker' / 'job' → SignalAndKill on the routed VMM,
//      then DestroyWithExport, then transition to STOPPED.
//   2. mode='service' → existing snapshotAndPark path, plus an
//      async convergeServiceReplicas fire-and-forget.
//   3. mode='request' (default) / 'mirror' → existing
//      snapshotAndPark path, no SignalAndKill call.
//
// The fakeVMM records both VMMClient and RoutedVMM shapes so we
// can assert that the worker/job path hits StopInstanceOnNode
// (routed) and the request/service path hits only the legacy
// snapshot/destroy surface. No firecracker involved — the
// portable surface here covers the engine's per-mode routing.
// End-to-end (PID 1 signal forwarding, guest-init reaper) is
// gated on //go:build metal and lands in commit 11.

package sched

import (
	"context"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// recordingStopVMM extends fakeVMM with M-2 / ADR-138 hooks:
// routed StopInstanceOnNode + DestroyWithExport counts. The
// per-mode dispatch in Engine.StopInstance hits different
// subsets of these surfaces depending on Instance.ExecutionMode,
// so the assertions below are mode-shaped.
type recordingStopVMM struct {
	*fakeVMM

	mu                  sync.Mutex
	stopInstanceOnNodeN int
	stopSignalLast      int32
	stopGraceLast       int32
}

func (r *recordingStopVMM) StopInstanceOnNode(_ context.Context, _, _ string, signal int32, grace int32) (*StopInstanceOutcome, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stopInstanceOnNodeN++
	r.stopSignalLast = signal
	r.stopGraceLast = grace
	return &StopInstanceOutcome{ExitCode: 0, KillSignalSent: false}, nil
}

// seedRunningInstanceWithMode seeds an app + deployment + a
// running instance stamped with the requested ExecutionMode.
// Uses CreateInstanceWithMode so the Instance.Mode field is
// populated at insert time, matching production. The instance
// is registered with the engine's NodeLedger via Admit so
// Engine.StopInstance's Release call doesn't trip the
// invariant §6.2-4 ledger check (Release on an unknown
// instance is a no-op, but having a real Admit matches the
// production ledger shape).
func seedRunningInstanceWithMode(t *testing.T, e *Engine, store state.Store, mode string) state.Instance {
	t.Helper()
	_, app, dep := seedApp(t, store, api.PlanPro, 256, 5)
	ins, err := store.CreateInstanceWithMode(context.Background(),
		app.ID, dep.ID, string(state.StateRunning), 256,
		"node-1", "wake-1", mode)
	if err != nil {
		t.Fatalf("CreateInstanceWithMode(%s): %v", mode, err)
	}
	if err := e.ledger.Admit(Request{
		Kind: KindWake, AppID: app.ID, DeploymentID: dep.ID, Plan: api.PlanPro,
		Instance: ins.ID, NodeID: "node-1", RAMMB: 256, VCPUBudget: 2,
		NodeCeilingMB: 47600,
	}); err != nil {
		t.Fatalf("ledger.Admit: %v", err)
	}
	return ins
}

// TestEngineStopInstance_WorkerUsesSignalAndKill pins the
// mode='worker' dispatch: routed StopInstanceOnNode + Destroy
// + transition to STOPPED. signal=0 → SIGTERM default; grace
// comes through verbatim from StopOptions.
func TestEngineStopInstance_WorkerUsesSignalAndKill(t *testing.T) {
	store := state.NewMemStore()
	rec := &recordingStopVMM{fakeVMM: &fakeVMM{}}
	e := newEngine(t, store, rec, &fakeNotifier{}, "1.10.0")
	ins := seedRunningInstanceWithMode(t, e, store, string(state.InstanceModeWorker))

	out, err := e.StopInstance(context.Background(), ins.ID, StopOptions{Signal: 0, GraceSeconds: 30})
	if err != nil {
		t.Fatalf("StopInstance: %v", err)
	}
	if out.Mode != string(state.InstanceModeWorker) {
		t.Errorf("out.Mode = %q; want worker", out.Mode)
	}
	if out.LifecycleReason != LifecycleReasonCleanExit {
		t.Errorf("LifecycleReason = %q; want clean_exit", out.LifecycleReason)
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if rec.stopInstanceOnNodeN != 1 {
		t.Errorf("StopInstanceOnNode = %d; want 1", rec.stopInstanceOnNodeN)
	}
	// signal=0 → engine falls back to SIGTERM (15).
	if rec.stopSignalLast != 15 {
		t.Errorf("signal = %d; want 15 (SIGTERM default)", rec.stopSignalLast)
	}
	if rec.stopGraceLast != 30 {
		t.Errorf("grace = %d; want 30", rec.stopGraceLast)
	}
	// Worker path must NOT snapshot (worker has no snapshot cache
	// semantics — see ADR-138 §Decision 1 rationale).
	if rec.snapshots != 0 {
		t.Errorf("snapshots = %d; want 0 (worker path skips Park)", rec.snapshots)
	}
	// Instance must be in STOPPED after the call.
	got, err := store.InstanceByID(context.Background(), ins.ID)
	if err != nil {
		t.Fatalf("InstanceByID: %v", err)
	}
	if got.State != string(state.StateStopped) {
		t.Errorf("State = %q; want STOPPED", got.State)
	}
}

// TestEngineStopInstance_JobUsesSignalAndKill is the symmetric
// pin for mode='job'. job also skips Park — both worker and
// job dispatch go through SignalAndKill + Destroy.
func TestEngineStopInstance_JobUsesSignalAndKill(t *testing.T) {
	store := state.NewMemStore()
	rec := &recordingStopVMM{fakeVMM: &fakeVMM{}}
	e := newEngine(t, store, rec, &fakeNotifier{}, "1.10.0")
	ins := seedRunningInstanceWithMode(t, e, store, string(state.InstanceModeJob))

	out, err := e.StopInstance(context.Background(), ins.ID, StopOptions{Signal: 0, GraceSeconds: 5})
	if err != nil {
		t.Fatalf("StopInstance: %v", err)
	}
	if out.Mode != string(state.InstanceModeJob) {
		t.Errorf("out.Mode = %q; want job", out.Mode)
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if rec.stopInstanceOnNodeN != 1 {
		t.Errorf("StopInstanceOnNode = %d; want 1", rec.stopInstanceOnNodeN)
	}
	if rec.snapshots != 0 {
		t.Errorf("snapshots = %d; want 0 (job path skips Park)", rec.snapshots)
	}
}

// TestEngineStopInstance_ServiceSnapshotsAndConverges pins
// mode='service': the snapshot cache must be preserved (Park),
// and convergeServiceReplicas must be triggered (best-effort).
// Today convergeServiceReplicas is a no-op stub (M-4 workstream
// E fills it in); this test asserts the snapshot path, not the
// convergence logic.
func TestEngineStopInstance_ServiceSnapshotsAndConverges(t *testing.T) {
	store := state.NewMemStore()
	rec := &recordingStopVMM{fakeVMM: &fakeVMM{}}
	e := newEngine(t, store, rec, &fakeNotifier{}, "1.10.0")
	ins := seedRunningInstanceWithMode(t, e, store, string(state.InstanceModeService))

	out, err := e.StopInstance(context.Background(), ins.ID, StopOptions{Signal: 0, GraceSeconds: 30})
	if err != nil {
		t.Fatalf("StopInstance: %v", err)
	}
	if out.Mode != string(state.InstanceModeService) {
		t.Errorf("out.Mode = %q; want service", out.Mode)
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	// service mode MUST NOT call StopInstanceOnNode — that's
	// reserved for worker/job (which destroy the snapshot).
	if rec.stopInstanceOnNodeN != 0 {
		t.Errorf("StopInstanceOnNode = %d; want 0 (service preserves snapshot)", rec.stopInstanceOnNodeN)
	}
	if rec.snapshots < 1 {
		t.Errorf("snapshots = %d; want >=1 (service preserves snapshot cache)", rec.snapshots)
	}
}

// TestEngineStopInstance_RequestUsesSnapshotAndPark pins the
// default (mode='request') dispatch: snapshotAndPark only,
// no SignalAndKill. Today's behaviour — this test guards
// against an accidental widening that turns the request path
// into the worker path.
func TestEngineStopInstance_RequestUsesSnapshotAndPark(t *testing.T) {
	store := state.NewMemStore()
	rec := &recordingStopVMM{fakeVMM: &fakeVMM{}}
	e := newEngine(t, store, rec, &fakeNotifier{}, "1.10.0")
	ins := seedRunningInstanceWithMode(t, e, store, string(state.InstanceModeNormal))

	out, err := e.StopInstance(context.Background(), ins.ID, StopOptions{Signal: 0, GraceSeconds: 30})
	if err != nil {
		t.Fatalf("StopInstance: %v", err)
	}
	if out.Mode != string(state.InstanceModeNormal) {
		t.Errorf("out.Mode = %q; want normal", out.Mode)
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if rec.stopInstanceOnNodeN != 0 {
		t.Errorf("StopInstanceOnNode = %d; want 0 (request preserves snapshot)", rec.stopInstanceOnNodeN)
	}
	if rec.snapshots < 1 {
		t.Errorf("snapshots = %d; want >=1", rec.snapshots)
	}
}

// TestEngineStopInstance_NotRunningReturnsZero pins the
// lockedRunning guard: a STOPPED instance must NOT enter the
// dispatch (Engine.StopInstance returns empty outcome, nil
// error — the caller decides what to do with "no work").
func TestEngineStopInstance_NotRunningReturnsZero(t *testing.T) {
	store := state.NewMemStore()
	rec := &recordingStopVMM{fakeVMM: &fakeVMM{}}
	e := newEngine(t, store, rec, &fakeNotifier{}, "1.10.0")
	_, app, dep := seedApp(t, store, api.PlanPro, 256, 5)
	ins, err := store.CreateInstanceWithMode(context.Background(),
		app.ID, dep.ID, string(state.StateStopped), 256,
		"node-1", "wake-1", string(state.InstanceModeWorker))
	if err != nil {
		t.Fatalf("CreateInstanceWithMode: %v", err)
	}

	out, err := e.StopInstance(context.Background(), ins.ID, StopOptions{GraceSeconds: 5})
	if err != nil {
		t.Fatalf("StopInstance: %v", err)
	}
	if out.Instance != "" || out.Mode != "" {
		t.Errorf("out = %+v; want zero-value (lockedRunning skipped)", out)
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if rec.stopInstanceOnNodeN != 0 {
		t.Errorf("StopInstanceOnNode = %d; want 0 (instance wasn't running)", rec.stopInstanceOnNodeN)
	}
}
