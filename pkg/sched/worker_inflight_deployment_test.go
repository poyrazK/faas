// adr: 20261018101500 — worker pools reconcile off the scheduler loop.
package sched_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// Prime publishes a deployment's first worker RUNNING before apid marks the
// deployment live. A pool reconcile in between must leave that worker alone,
// not stop it as a retired generation; once the deployment has failed, the
// same reconcile stops it.
func TestWorkerPoolLeavesAnInFlightDeploymentsWorker(t *testing.T) {
	ctx := t.Context()
	store := state.NewMemStore()
	_, app, _ := sched.SeedWorkerScopes(t, store, api.PlanPro)
	next, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "default",
		Kind: state.DeploymentKindImage, ImageDigest: "sha256:next"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateDeploymentStatus(ctx, next.ID, state.DeploySnapshotting, ""); err != nil {
		t.Fatal(err)
	}
	if next, err = store.DeploymentByID(ctx, next.ID); err != nil {
		t.Fatal(err)
	}
	prime := sched.SeedScopedWorker(t, store, app, next)
	engine := sched.XNewEngine(t, store, &sched.FakeVMM{}, &sched.FakeNotifier{}, "1.10.0").WithWorkerScaleInStabilization(0)
	if err := engine.SeedLedger(ctx); err != nil {
		t.Fatal(err)
	}
	stateOf := func() state.State {
		t.Helper()
		ins, err := store.InstanceByID(ctx, prime.ID)
		if err != nil {
			t.Fatal(err)
		}
		return state.State(ins.State)
	}

	if err := engine.ReconcileWorkerPools(ctx, app.ID, sched.TriggerWorkerPool); err != nil {
		t.Fatal(err)
	}
	engine.WaitWorkerStops()
	if got := stateOf(); got != state.StateRunning {
		t.Fatalf("worker of the deployment being primed is %s; the pool reconcile must leave it RUNNING", got)
	}

	if err := store.UpdateDeploymentStatus(ctx, next.ID, state.DeployFailed, "prime failed"); err != nil {
		t.Fatal(err)
	}
	if err := engine.ReconcileWorkerPools(ctx, app.ID, sched.TriggerWorkerPool); err != nil {
		t.Fatal(err)
	}
	engine.WaitWorkerStops()
	if got := stateOf(); got.CountsForRAM() {
		t.Fatalf("worker of a failed deployment is %s; the pool reconcile must stop it", got)
	}
}
