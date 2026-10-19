// adr: 20261019103000 — a worker pool freezes surplus workers and thaws them
// before booting new ones.
package sched

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func newFrozenWorkerEngine(t *testing.T, plan api.Plan, workersSwitch bool) (*state.MemStore, *frozenFakeVMM, *Engine, state.Account, state.App) {
	t.Helper()
	store := state.NewMemStore()
	acct, app, _ := seedWorkerScopes(t, store, plan)
	vmm := &frozenFakeVMM{fakeVMM: &fakeVMM{}}
	engine := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0").WithWorkerScaleInStabilization(0)
	engine.EnableFrozenTier()
	if workersSwitch {
		engine.EnableFrozenTierWorkers()
	}
	if err := engine.SeedLedger(t.Context()); err != nil {
		t.Fatal(err)
	}
	return store, vmm, engine, acct, app
}

func workerRows(t *testing.T, store state.Store, appID, scope string) []state.Instance {
	t.Helper()
	instances, err := store.ListInstancesForApp(context.Background(), appID)
	if err != nil {
		t.Fatal(err)
	}
	var out []state.Instance
	for _, ins := range instances {
		dep, err := store.DeploymentByID(context.Background(), ins.DeploymentID)
		if err != nil {
			t.Fatal(err)
		}
		if ins.Mode == string(state.InstanceModeWorker) && normalizedDeploymentScope(dep.Scope) == scope {
			out = append(out, ins)
		}
	}
	return out
}

func cancelDemand(t *testing.T, store state.Store, rows []state.Invocation) {
	t.Helper()
	for _, row := range rows {
		if err := store.CancelInvocation(context.Background(), row.ID); err != nil {
			t.Fatal(err)
		}
	}
}

func TestWorkerPoolFreezesSurplusWorkerAndThawsItForDemand(t *testing.T) {
	ctx := t.Context()
	store, vmm, engine, acct, app := newFrozenWorkerEngine(t, api.PlanPro, true)
	demand := enqueueScopedDemand(t, store, acct, app, "default", "", 10)
	if err := engine.ReconcileWorkerPools(ctx, app.ID, TriggerWorkerPool); err != nil {
		t.Fatal(err)
	}
	booted := workerRows(t, store, app.ID, "default")
	if len(booted) != 1 || booted[0].State != string(state.StateRunning) || vmm.coldBoots != 1 {
		t.Fatalf("scale out from zero: rows=%+v cold boots=%d", booted, vmm.coldBoots)
	}

	// The queue drains: the surplus worker freezes instead of stopping.
	cancelDemand(t, store, demand)
	if err := engine.ReconcileWorkerPools(ctx, app.ID, TriggerWorkerPool); err != nil {
		t.Fatal(err)
	}
	engine.WaitWorkerStops()
	rows := workerRows(t, store, app.ID, "default")
	if len(rows) != 1 || rows[0].ID != booted[0].ID || rows[0].State != string(state.StateFrozen) || vmm.freezes != 1 {
		t.Fatalf("scale in: rows=%+v freezes=%d; the surplus worker must freeze", rows, vmm.freezes)
	}
	if rows[0].LastRequestAt.IsZero() {
		t.Fatal("frozen worker has no freeze time for FrozenTTL")
	}
	// Frozen, the pool's next reconcile keeps it rather than stopping it.
	if err := engine.ReconcileWorkerPools(ctx, app.ID, TriggerWorkerPool); err != nil {
		t.Fatal(err)
	}
	if rows := workerRows(t, store, app.ID, "default"); rows[0].State != string(state.StateFrozen) {
		t.Fatalf("idle reconcile moved the frozen worker to %s", rows[0].State)
	}

	// Demand returns: the same worker thaws and no VM boots.
	enqueueScopedDemand(t, store, acct, app, "default", "", 10)
	if err := engine.ReconcileWorkerPools(ctx, app.ID, TriggerWorkerPool); err != nil {
		t.Fatal(err)
	}
	rows = workerRows(t, store, app.ID, "default")
	if len(rows) != 1 || rows[0].ID != booted[0].ID || rows[0].State != string(state.StateRunning) || vmm.thaws != 1 || vmm.coldBoots != 1 {
		t.Fatalf("scale out with a frozen worker: rows=%+v thaws=%d cold boots=%d; the frozen worker must thaw", rows, vmm.thaws, vmm.coldBoots)
	}
}

func TestWorkerPoolStopsSurplusWorkerWithoutTheWorkersSwitch(t *testing.T) {
	ctx := t.Context()
	store, vmm, engine, acct, app := newFrozenWorkerEngine(t, api.PlanPro, false)
	demand := enqueueScopedDemand(t, store, acct, app, "default", "", 10)
	if err := engine.ReconcileWorkerPools(ctx, app.ID, TriggerWorkerPool); err != nil {
		t.Fatal(err)
	}
	cancelDemand(t, store, demand)
	if err := engine.ReconcileWorkerPools(ctx, app.ID, TriggerWorkerPool); err != nil {
		t.Fatal(err)
	}
	engine.WaitWorkerStops()
	for _, row := range workerRows(t, store, app.ID, "default") {
		if state.State(row.State).CountsForRAM() {
			t.Fatalf("worker %s is %s; with frozen_tier_workers off a surplus worker stops", row.ID, row.State)
		}
	}
	if vmm.freezes != 0 {
		t.Fatalf("froze %d workers with frozen_tier_workers off", vmm.freezes)
	}
}

// A frozen worker is still resident, so it keeps its account worker slot
// until a pool elsewhere needs it: Hobby's single slot then goes to the
// environment with demand and the frozen worker stops.
func TestFrozenWorkerGivesUpItsSlotToDemandElsewhere(t *testing.T) {
	ctx := t.Context()
	store, vmm, engine, acct, app := newFrozenWorkerEngine(t, api.PlanHobby, true)
	staging := enqueueScopedDemand(t, store, acct, app, "staging", "", 10)
	if err := engine.ReconcileWorkerPools(ctx, app.ID, TriggerWorkerPool); err != nil {
		t.Fatal(err)
	}
	cancelDemand(t, store, staging)
	if err := engine.ReconcileWorkerPools(ctx, app.ID, TriggerWorkerPool); err != nil {
		t.Fatal(err)
	}
	engine.WaitWorkerStops()
	if rows := workerRows(t, store, app.ID, "staging"); len(rows) != 1 || rows[0].State != string(state.StateFrozen) {
		t.Fatalf("staging worker = %+v, want one frozen", rows)
	}
	enqueueScopedDemand(t, store, acct, app, "default", "", 10)
	if err := engine.ReconcileWorkerPools(ctx, app.ID, TriggerWorkerPool); err != nil {
		t.Fatal(err)
	}
	rows := workerRows(t, store, app.ID, "default")
	if len(rows) != 1 || rows[0].State != string(state.StateRunning) || vmm.coldBoots != 2 {
		t.Fatalf("default worker = %+v, cold boots=%d; demand must take the frozen worker's slot", rows, vmm.coldBoots)
	}
	if staged := workerRows(t, store, app.ID, "staging"); len(staged) != 1 || staged[0].State != string(state.StateStopped) {
		t.Fatalf("staging worker = %+v; giving up its slot must stop the frozen worker", staged)
	}
}
