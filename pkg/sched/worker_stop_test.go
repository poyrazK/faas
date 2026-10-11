package sched

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// blockingStopVMM holds every StopInstanceOnNode call until release is
// closed, standing in for a workload that uses its whole grace period.
type blockingStopVMM struct {
	*fakeVMM
	started chan string
	release chan struct{}

	mu    sync.Mutex
	stops int
}

func newBlockingStopVMM() *blockingStopVMM {
	return &blockingStopVMM{fakeVMM: &fakeVMM{}, started: make(chan string, 16), release: make(chan struct{})}
}

func (b *blockingStopVMM) StopInstanceOnNode(_ context.Context, _, instance string, _ int32, _ int32) (*StopInstanceOutcome, error) {
	b.mu.Lock()
	b.stops++
	b.mu.Unlock()
	b.started <- instance
	<-b.release
	return &StopInstanceOutcome{}, nil
}

func (b *blockingStopVMM) stopCalls() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.stops
}

func (b *blockingStopVMM) destroyCalls() int {
	b.fakeVMM.mu.Lock()
	defer b.fakeVMM.mu.Unlock()
	return b.fakeVMM.destroys
}

func awaitStopStarted(t *testing.T, vmm *blockingStopVMM) string {
	t.Helper()
	select {
	case id := <-vmm.started:
		return id
	case <-time.After(5 * time.Second):
		t.Fatal("background worker stop never reached vmmd")
		return ""
	}
}

func seedBlockedWorkerStop(t *testing.T) (*Engine, *blockingStopVMM, state.Store, state.App, map[string]state.Deployment, state.Instance) {
	t.Helper()
	ctx := context.Background()
	store := state.NewMemStore()
	_, app, deps := seedWorkerScopes(t, store, api.PlanPro)
	worker := seedScopedWorker(t, store, app, deps["default"])
	vmm := newBlockingStopVMM()
	engine := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")
	if err := engine.SeedLedger(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		select {
		case <-vmm.release:
		default:
			close(vmm.release)
		}
		engine.WaitWorkerStops()
	})
	reconciled := make(chan error, 1)
	go func() { reconciled <- engine.ReconcileWorkerPoolForScope(ctx, app.ID, "default", 0, TriggerWorkerPool) }()
	select {
	case err := <-reconciled:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("reconcile waited for the worker's grace period")
	}
	if got := awaitStopStarted(t, vmm); got != worker.ID {
		t.Fatalf("stopped %s, want %s", got, worker.ID)
	}
	return engine, vmm, store, app, deps, worker
}

func TestWorkerStopDoesNotHoldReconcileOrAppLock(t *testing.T) {
	ctx := context.Background()
	engine, vmm, store, app, _, worker := seedBlockedWorkerStop(t)

	// The reconcile returned while the workload is still inside its grace
	// period, and the app lock that admission needs is free.
	locked := make(chan struct{})
	go func() {
		release := engine.lockApp(app.ID)
		release()
		close(locked)
	}()
	select {
	case <-locked:
	case <-time.After(5 * time.Second):
		t.Fatal("app lock held across the worker's grace period")
	}
	if got, _ := store.InstanceByID(ctx, worker.ID); got.State != string(state.StateRunning) {
		t.Fatalf("worker left RUNNING before its grace period ended: %s", got.State)
	}

	// A later tick neither keeps nor re-stops the worker it already owns.
	if err := engine.ReconcileWorkerPoolForScope(ctx, app.ID, "default", 0, TriggerWorkerPool); err != nil {
		t.Fatal(err)
	}
	if vmm.stopCalls() != 1 {
		t.Fatalf("worker stop issued %d times", vmm.stopCalls())
	}

	close(vmm.release)
	engine.WaitWorkerStops()
	if got, _ := store.InstanceByID(ctx, worker.ID); got.State != string(state.StateStopped) || vmm.destroyCalls() != 1 {
		t.Fatalf("teardown state=%s destroys=%d", got.State, vmm.destroyCalls())
	}
	if engine.ledger.Concurrency(app.ID) != 0 {
		t.Fatalf("ledger still holds the stopped worker: %d", engine.ledger.Concurrency(app.ID))
	}
}

func TestStoppingWorkerStillCountsAgainstAccountCap(t *testing.T) {
	ctx := context.Background()
	engine, vmm, _, app, deps, _ := seedBlockedWorkerStop(t)
	// Pro allows three workers per account. Two neighbors plus the worker
	// still inside its grace period fill it.
	seedScopedWorker(t, engine.store, app, deps["staging"])
	seedScopedWorker(t, engine.store, app, deps["staging"])
	if err := engine.SeedLedger(ctx); err != nil {
		t.Fatal(err)
	}
	if err := engine.ReconcileWorkerPoolForScope(ctx, app.ID, "default", 1, TriggerWorkerPool); err != nil {
		t.Fatal(err)
	}
	if vmm.coldBoots != 0 {
		t.Fatalf("admitted a replacement while the stopping worker held the last slot: boots=%d", vmm.coldBoots)
	}
}

func TestWorkerStopTeardownYieldsToAnotherOwner(t *testing.T) {
	ctx := context.Background()
	engine, vmm, store, app, _, worker := seedBlockedWorkerStop(t)
	// A runtime-config refresh withdraws the row while the signal is out.
	if _, err := engine.transitionWithKindCAS(ctx, worker.ID, app.ID, state.StateDraining, "runtime_config_restart", "test"); err != nil {
		t.Fatal(err)
	}
	close(vmm.release)
	engine.WaitWorkerStops()
	if got, _ := store.InstanceByID(ctx, worker.ID); got.State != string(state.StateDraining) || vmm.destroyCalls() != 0 {
		t.Fatalf("background stop took over a withdrawn row: state=%s destroys=%d", got.State, vmm.destroyCalls())
	}
}
