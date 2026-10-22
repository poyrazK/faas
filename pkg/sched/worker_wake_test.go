// adr: 079 — a parked worker has a customer path back to running.
package sched_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestWakeWorkerAppReactivatesParkedWorker(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	_, app, dep := sched.SeedApp(t, store, api.PlanPro, 256, 4)
	sched.ConfigureExecutionMode(t, store, app, dep, api.ExecutionModeWorker, state.DeployLive)
	parked := state.AppEvictedCold
	if _, err := store.UpdateApp(ctx, app.ID, state.UpdateAppParams{Status: &parked}); err != nil {
		t.Fatal(err)
	}
	engine := sched.XNewEngine(t, store, &sched.FakeVMM{}, &sched.FakeNotifier{}, "1.10.0")
	wakeID := uuid.NewString()

	handled, err := engine.WakeWorkerApp(sched.WithRequestedWakeID(ctx, wakeID), app.ID)
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
	_, app, _ := sched.SeedApp(t, store, api.PlanPro, 256, 4)
	engine := sched.XNewEngine(t, store, &sched.FakeVMM{}, &sched.FakeNotifier{}, "1.10.0")
	if handled, err := engine.WakeWorkerApp(context.Background(), app.ID); handled || err != nil {
		t.Fatalf("request app handled=%v err=%v; want EnsureWake path", handled, err)
	}
}

// TestEnsureWakeNamesTheMissingRequestListener pins production-us hunt #9,
// H9-169. The public URL of a worker-class app answered every request with
// "500 schedd operation failed: sched: at capacity". The refusal must say why
// and stay an at-capacity refusal; the first case is the shape a portless
// compose `build:` service gets (worker class, no execution mode).
func TestEnsureWakeNamesTheMissingRequestListener(t *testing.T) {
	cases := []struct {
		name  string
		setup func(*testing.T, *state.MemStore, state.App, state.Deployment)
	}{
		{"worker class without an execution mode", func(t *testing.T, store *state.MemStore, app state.App, _ state.Deployment) {
			if _, err := store.SetAppWorkloadClass(context.Background(), app.ID, state.WorkloadClassWorker, "scan_hint"); err != nil {
				t.Fatal(err)
			}
		}},
		{"worker execution mode", func(t *testing.T, store *state.MemStore, app state.App, dep state.Deployment) {
			sched.ConfigureExecutionMode(t, store, app, dep, api.ExecutionModeWorker, state.DeployLive)
		}},
		{"job execution mode", func(t *testing.T, store *state.MemStore, app state.App, dep state.Deployment) {
			sched.ConfigureExecutionMode(t, store, app, dep, api.ExecutionModeJob, state.DeployLive)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := state.NewMemStore()
			_, app, dep := sched.SeedApp(t, store, api.PlanPro, 256, 4)
			tc.setup(t, store, app, dep)
			engine := sched.XNewEngine(t, store, &sched.FakeVMM{}, &sched.FakeNotifier{}, "1.10.0")

			out, err := engine.EnsureWake(context.Background(), app.ID, sched.TriggerGateway)
			if !errors.Is(err, sched.ErrNoRequestListener) || !errors.Is(out.Err, sched.ErrNoRequestListener) {
				t.Fatalf("EnsureWake = %v (out.Err %v); want ErrNoRequestListener", err, out.Err)
			}
			if !errors.Is(err, sched.ErrAtCapacity) {
				t.Fatalf("EnsureWake = %v; want it to stay an at-capacity refusal", err)
			}
			instances, err := store.ListInstancesForApp(context.Background(), app.ID)
			if err != nil || len(instances) != 0 {
				t.Fatalf("instances = %+v, %v; want none", instances, err)
			}
		})
	}
}
