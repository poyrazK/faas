// adr: 079 — a parked worker has a customer path back to running.
package sched

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestWakeWorkerAppReactivatesParkedWorker(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	_, app, dep := seedApp(t, store, api.PlanPro, 256, 4)
	configureExecutionMode(t, store, app, dep, api.ExecutionModeWorker, state.DeployLive)
	parked := state.AppEvictedCold
	if _, err := store.UpdateApp(ctx, app.ID, state.UpdateAppParams{Status: &parked}); err != nil {
		t.Fatal(err)
	}
	engine := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0")
	wakeID := uuid.NewString()

	handled, err := engine.WakeWorkerApp(withRequestedWakeID(ctx, wakeID), app.ID)
	if !handled || err != nil {
		t.Fatalf("WakeWorkerApp = %v, %v; want handled", handled, err)
	}
	after, err := store.AppByID(ctx, app.ID)
	if err != nil || after.Status != state.AppActive {
		t.Fatalf("app status = %q, %v; want active", after.Status, err)
	}
	instances, err := store.ListInstancesForApp(ctx, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, ins := range instances {
		if ins.DeploymentID == dep.ID && ins.Mode == string(state.InstanceModeWorker) &&
			state.State(ins.State).CountsForConcurrency() && ins.WakeID == wakeID {
			return
		}
	}
	t.Fatalf("no live worker carrying wake %s: %+v", wakeID, instances)
}

func TestWakeWorkerAppLeavesRequestAppsToEnsureWake(t *testing.T) {
	store := state.NewMemStore()
	_, app, _ := seedApp(t, store, api.PlanPro, 256, 4)
	engine := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0")
	if handled, err := engine.WakeWorkerApp(context.Background(), app.ID); handled || err != nil {
		t.Fatalf("request app handled=%v err=%v; want EnsureWake path", handled, err)
	}
}
