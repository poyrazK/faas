package sched

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

func seedPinnedWorkerProject(t *testing.T) (*state.MemStore, state.App, state.Deployment, state.Deployment, state.Invocation) {
	t.Helper()
	ctx := t.Context()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "pinned-worker@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "pinned-worker"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "pinned-worker", WorkloadName: "worker", Type: state.AppTypeApp, WorkloadClass: state.WorkloadClassWorker, RAMMB: 256, MaxConcurrency: 1, Manifest: state.AppManifest{ExecutionMode: api.ExecutionModeWorker, RevisionPinTTLSeconds: 3600}})
	if err != nil {
		t.Fatal(err)
	}
	create := func(name string) state.Deployment {
		dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "production", Kind: state.DeploymentKindImage, ImageDigest: "sha256:" + name})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.SetDeploymentRootfs(ctx, dep.ID, "/"+name+".ext4", "layers/"+dep.ID, 4096); err != nil {
			t.Fatal(err)
		}
		if err := store.MarkDeploymentLive(ctx, dep.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := store.PublishProjectReleaseSet(ctx, account.ID, project.ID, "production", 1800, []state.ProjectReleaseMember{{AppID: app.ID, DeploymentID: dep.ID}}); err != nil {
			t.Fatal(err)
		}
		dep, err = store.DeploymentByID(ctx, dep.ID)
		if err != nil {
			t.Fatal(err)
		}
		return dep
	}
	old := create("old")
	binding, err := store.CreateQueueBindingWithConsumer(ctx, state.QueueBinding{AccountID: account.ID, AppID: app.ID, DeploymentScope: "production", Name: "orders", QueueName: "orders", Mode: "push", Enabled: true, WorkloadClass: state.WorkloadClassWorker, MaxConcurrency: 2})
	if err != nil {
		t.Fatal(err)
	}
	inv, err := store.EnqueueInvocation(ctx, state.Invocation{AccountID: account.ID, AppID: app.ID, Source: state.InvocationQueue, QueueBindingID: binding.Binding.ID, DeploymentScope: "production", QueueName: "orders", DueAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	inv, err = store.ClaimInvocation(ctx, inv.ID, "", 60)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := state.ResolveInvocationVersion(ctx, store, inv); err != nil {
		t.Fatal(err)
	}
	return store, app, old, create("candidate"), inv
}

func TestProjectPinnedWorkerRestartAndDrain(t *testing.T) {
	store, app, old, candidate, inv := seedPinnedWorkerProject(t)
	ctx := t.Context()
	resident := seedScopedWorker(t, store, app, old)
	vmm := &recordingStopVMM{fakeVMM: &fakeVMM{}}
	e := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")
	if err := e.SeedLedger(ctx); err != nil {
		t.Fatal(err)
	}
	if err := e.ReconcileWorkerPools(ctx, app.ID, TriggerWorkerPool); err != nil {
		t.Fatal(err)
	}
	e.WaitWorkerStops()
	check := func(engine *Engine) {
		t.Helper()
		rows, err := store.ListInstancesForApp(ctx, app.ID)
		if err != nil {
			t.Fatal(err)
		}
		counts := map[string]int{}
		for _, row := range rows {
			if state.State(row.State).CountsForConcurrency() {
				counts[row.DeploymentID]++
			}
		}
		if counts[old.ID] != 1 || counts[candidate.ID] != 1 || engine.ledger.Concurrency(app.ID) != 1+api.RolloutConcurrencyGrant {
			t.Fatalf("worker overlap/cap broken: %v ledger=%d", counts, engine.ledger.Concurrency(app.ID))
		}
	}
	check(e)
	// Simulate a lost old resident and a schedd restart. The durable pin must
	// reconstruct the old pool even while the candidate is already serving.
	if err := store.UpdateInstanceState(ctx, resident.ID, string(state.StateStopped)); err != nil {
		t.Fatal(err)
	}
	e = newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")
	if err := e.SeedLedger(ctx); err != nil {
		t.Fatal(err)
	}
	if err := e.ReconcileWorkerPools(ctx, app.ID, TriggerWorkerPool); err != nil {
		t.Fatal(err)
	}
	check(e)
	_, version, err := state.ResolveInvocationVersion(ctx, store, inv)
	if err != nil || version.DeploymentID != old.ID {
		t.Fatalf("restart changed job deployment: %+v %v", version, err)
	}
	if err := store.CompleteInvocation(ctx, inv.ID, nil); err != nil {
		t.Fatal(err)
	}
	if err := e.ReconcileWorkerPools(ctx, app.ID, TriggerWorkerPool); err != nil {
		t.Fatal(err)
	}
	e.WaitWorkerStops()
	rows, err := store.ListInstancesForApp(ctx, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.DeploymentID == old.ID && state.State(row.State).CountsForRAM() {
			t.Fatalf("terminal job held old worker: %+v", row)
		}
	}
	if e.ledger.Concurrency(app.ID) != 1 {
		t.Fatalf("overlap grant did not retire: %d", e.ledger.Concurrency(app.ID))
	}
}

type failedPinnedWorkerRead struct{ MemStore *state.MemStore }

func (s failedPinnedWorkerRead) PinnedProjectReleaseWorkers(context.Context, string) ([]string, error) {
	return nil, errors.New("pin read unavailable")
}

func TestProjectPinnedWorkerReadFailureDoesNotMutateFleet(t *testing.T) {
	store, app, old, _, _ := seedPinnedWorkerProject(t)
	resident := seedScopedWorker(t, store, app, old)
	vmm := &recordingStopVMM{fakeVMM: &fakeVMM{}}
	e := newEngine(t, statetest.NewMem(failedPinnedWorkerRead{MemStore: store}), vmm, &fakeNotifier{}, "1.10.0")
	if err := e.SeedLedger(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := e.ReconcileWorkerPools(t.Context(), app.ID, TriggerWorkerPool); err == nil {
		t.Fatal("missing evidence authorized mutation")
	}
	e.WaitWorkerStops()
	row, err := store.InstanceByID(t.Context(), resident.ID)
	if err != nil || row.State != string(state.StateRunning) || vmm.coldBoots != 0 || vmm.stopInstanceOnNodeN != 0 {
		t.Fatalf("read failure changed fleet: %+v %v", row, err)
	}
}
