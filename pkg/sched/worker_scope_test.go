// adr: 568 — environment intent and runtime ownership contracts.
package sched

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

func seedWorkerScopes(t *testing.T, store state.Store, plan api.Plan) (state.Account, state.App, map[string]state.Deployment) {
	t.Helper()
	ctx := context.Background()
	acct, err := store.CreateAccount(ctx, "worker-scopes@example.test", plan)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "worker-scopes", Type: state.AppTypeApp,
		WorkloadClass: state.WorkloadClassWorker, Status: state.AppActive, RAMMB: 256, MaxConcurrency: 5,
		Manifest:      state.AppManifest{ExecutionMode: api.ExecutionModeWorker, WorkerReplicas: &state.WorkerScaling{Min: 0}},
		ScalingPolicy: &state.ScalingPolicy{Targets: []state.ScalingTarget{{Metric: api.ScalingMetricQueueDepth, Value: 10}}}})
	if err != nil {
		t.Fatal(err)
	}
	// App creation and policy mutation are separate public intent operations.
	policy := &state.ScalingPolicy{Targets: []state.ScalingTarget{{Metric: api.ScalingMetricQueueDepth, Value: 10}}}
	app, err = store.UpdateApp(ctx, app.ID, state.UpdateAppParams{ScalingPolicy: policy, SetScalingPolicy: true})
	if err != nil {
		t.Fatal(err)
	}
	deps := map[string]state.Deployment{}
	for _, scope := range []string{"default", "staging"} {
		dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: scope,
			Kind: state.DeploymentKindImage, ImageDigest: "sha256:" + scope, Status: state.DeployLive})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.UpdateDeploymentStatus(ctx, dep.ID, state.DeployLive, ""); err != nil {
			t.Fatal(err)
		}
		dep, err = store.DeploymentByID(ctx, dep.ID)
		if err != nil {
			t.Fatal(err)
		}
		deps[scope] = dep
	}
	return acct, app, deps
}

func seedScopedWorker(t *testing.T, store state.Store, app state.App, dep state.Deployment) state.Instance {
	t.Helper()
	node, err := store.ComputeNodeByName(context.Background(), state.DefaultLocalNodeName)
	if err != nil {
		t.Fatal(err)
	}
	ins, err := store.CreateInstanceWithMode(context.Background(), app.ID, dep.ID,
		string(state.StateRunning), app.RAMMB, node.ID, uuid.NewString(), string(state.InstanceModeWorker))
	if err != nil {
		t.Fatal(err)
	}
	return ins
}

func scopedWorkerCounts(t *testing.T, store state.Store, appID string) map[string]int {
	t.Helper()
	instances, err := store.ListInstancesForApp(context.Background(), appID)
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, ins := range instances {
		if ins.Mode != string(state.InstanceModeWorker) || !state.State(ins.State).CountsForConcurrency() {
			continue
		}
		dep, err := store.DeploymentByID(context.Background(), ins.DeploymentID)
		if err != nil {
			t.Fatal(err)
		}
		counts[normalizedDeploymentScope(dep.Scope)]++
	}
	return counts
}

func enqueueScopedDemand(t *testing.T, store state.Store, acct state.Account, app state.App, scope, queue string, count int) []state.Invocation {
	t.Helper()
	rows := []state.Invocation{}
	for range count {
		row, err := store.EnqueueInvocation(context.Background(), state.Invocation{AccountID: acct.ID, AppID: app.ID,
			DeploymentScope: scope, QueueName: queue, Source: state.InvocationQueue, DueAt: time.Now()})
		if err != nil {
			t.Fatal(err)
		}
		rows = append(rows, row)
	}
	return rows
}

func TestWorkerScopedPoolChangesOnlySelectedEnvironment(t *testing.T) {
	for _, scope := range []string{"default", "staging"} {
		t.Run(scope, func(t *testing.T) {
			ctx := context.Background()
			store := state.NewMemStore()
			_, app, deps := seedWorkerScopes(t, store, api.PlanPro)
			selected := seedScopedWorker(t, store, app, deps[scope])
			neighborScope := "default"
			if scope == neighborScope {
				neighborScope = "staging"
			}
			neighbor := seedScopedWorker(t, store, app, deps[neighborScope])
			vmm := &recordingStopVMM{fakeVMM: &fakeVMM{}}
			engine := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")
			if err := engine.SeedLedger(ctx); err != nil {
				t.Fatal(err)
			}
			if err := engine.ReconcileWorkerPoolForScope(ctx, app.ID, scope, 0, TriggerWorkerPool); err != nil {
				t.Fatal(err)
			}
			engine.WaitWorkerStops()
			after, _ := store.InstanceByID(ctx, selected.ID)
			other, _ := store.InstanceByID(ctx, neighbor.ID)
			if after.State != string(state.StateStopped) || other.State != string(state.StateRunning) || vmm.stopInstanceOnNodeN != 1 {
				t.Fatalf("states selected=%s neighbor=%s, stops=%d", after.State, other.State, vmm.stopInstanceOnNodeN)
			}
			if err := engine.ReconcileWorkerPoolForScope(ctx, app.ID, scope, 2, TriggerWorkerPool); err != nil {
				t.Fatal(err)
			}
			engine.WaitWorkerStops()
			counts := scopedWorkerCounts(t, store, app.ID)
			if counts[scope] != 2 || counts[neighborScope] != 1 || engine.ledger.Concurrency(app.ID) != 3 {
				t.Fatalf("scoped scale-out = %v, ledger=%d", counts, engine.ledger.Concurrency(app.ID))
			}
		})
	}
}

func testWorkerScopedDemandLifecycle(t *testing.T, store state.Store) {
	t.Helper()
	ctx := context.Background()
	acct, app, deps := seedWorkerScopes(t, store, api.PlanPro)
	seedScopedWorker(t, store, app, deps["staging"])
	defaultRows := enqueueScopedDemand(t, store, acct, app, "default", "", 21)
	enqueueScopedDemand(t, store, acct, app, "staging", "", 1)
	vmm := &recordingStopVMM{fakeVMM: &fakeVMM{}}
	engine := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")
	if err := engine.SeedLedger(ctx); err != nil {
		t.Fatal(err)
	}
	if err := engine.ReconcileWorkerPools(ctx, app.ID, TriggerWorkerPool); err != nil {
		t.Fatal(err)
	}
	engine.WaitWorkerStops()
	// Pro's three-worker account cap includes staging's existing resident.
	counts := scopedWorkerCounts(t, store, app.ID)
	if counts["default"] != 2 || counts["staging"] != 1 || vmm.coldBoots != 2 {
		t.Fatalf("captured demand allocation = %v, boots=%d", counts, vmm.coldBoots)
	}
	for _, row := range defaultRows {
		if err := store.CancelInvocation(ctx, row.ID); err != nil {
			t.Fatal(err)
		}
	}
	// Lifecycle notifications use the same scoped demand contract as ticks.
	engine.ReconcileWorkerApp(ctx, app.ID)
	engine.WaitWorkerStops()
	counts = scopedWorkerCounts(t, store, app.ID)
	if counts["default"] != 0 || counts["staging"] != 1 || vmm.stopInstanceOnNodeN != 2 {
		t.Fatalf("scoped scale-in = %v, stops=%d", counts, vmm.stopInstanceOnNodeN)
	}
}

func TestWorkerScopedDemandLifecycle(t *testing.T) {
	testWorkerScopedDemandLifecycle(t, state.NewMemStore())
}

func TestWorkerScopedCooldownDoesNotHoldColdNeighbor(t *testing.T) {
	store := state.NewMemStore()
	acct, app, deps := seedWorkerScopes(t, store, api.PlanPro)
	policy := app.ScalingPolicy
	policy.ScaleOutCooldownS = 60
	if _, err := store.UpdateApp(context.Background(), app.ID, state.UpdateAppParams{ScalingPolicy: policy, SetScalingPolicy: true}); err != nil {
		t.Fatal(err)
	}
	seedScopedWorker(t, store, app, deps["default"])
	enqueueScopedDemand(t, store, acct, app, "default", "", 21)
	enqueueScopedDemand(t, store, acct, app, "staging", "", 11)
	ops := wire.NewOpsMetrics("schedd")
	engine := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0").WithOpsMetrics(ops)
	if err := engine.SeedLedger(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := engine.ReconcileWorkerPools(context.Background(), app.ID, TriggerWorkerPool); err != nil {
		t.Fatal(err)
	}
	engine.WaitWorkerStops()
	counts := scopedWorkerCounts(t, store, app.ID)
	if counts["default"] != 1 || counts["staging"] != 2 {
		t.Fatalf("cooldown crossed environment boundary: %v", counts)
	}
	if got := scopedWorkerCounter(t, ops, "schedd_scale_up_decisions_total", map[string]string{"app": app.ID, "outcome": "cooldown_held"}); got != 1 {
		t.Fatalf("scoped cooldown observation = %v", got)
	}
}

func TestWorkerScopedCooldownRetainsTerminationHistory(t *testing.T) {
	store := state.NewMemStore()
	acct, app, deps := seedWorkerScopes(t, store, api.PlanPro)
	policy := app.ScalingPolicy
	policy.ScaleInCooldownS = 60
	if _, err := store.UpdateApp(context.Background(), app.ID, state.UpdateAppParams{ScalingPolicy: policy, SetScalingPolicy: true}); err != nil {
		t.Fatal(err)
	}
	seedScopedWorker(t, store, app, deps["default"])
	seedScopedWorker(t, store, app, deps["default"])
	rows := enqueueScopedDemand(t, store, acct, app, "default", "", 1)
	vmm := &recordingStopVMM{fakeVMM: &fakeVMM{}}
	engine := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")
	if err := engine.SeedLedger(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := engine.ReconcileWorkerPools(context.Background(), app.ID, TriggerWorkerPool); err != nil {
		t.Fatal(err)
	}
	engine.WaitWorkerStops()
	if scopedWorkerCounts(t, store, app.ID)["default"] != 1 || vmm.stopInstanceOnNodeN != 1 {
		t.Fatal("first scale-in should not inherit an admission cooldown")
	}
	if err := store.CancelInvocation(context.Background(), rows[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := engine.ReconcileWorkerPools(context.Background(), app.ID, TriggerWorkerPool); err != nil {
		t.Fatal(err)
	}
	engine.WaitWorkerStops()
	if scopedWorkerCounts(t, store, app.ID)["default"] != 1 || vmm.stopInstanceOnNodeN != 1 {
		t.Fatal("removed replica's termination did not hold the next scale-in")
	}
	engine.now = func() time.Time { return time.Now().Add(61 * time.Second) }
	if err := engine.ReconcileWorkerPools(context.Background(), app.ID, TriggerWorkerPool); err != nil {
		t.Fatal(err)
	}
	engine.WaitWorkerStops()
	if scopedWorkerCounts(t, store, app.ID)["default"] != 0 || vmm.stopInstanceOnNodeN != 2 {
		t.Fatal("expired scoped cooldown retained an empty pool")
	}
}

func TestWorkerScopedPoolRetiresOnlySelectedOldGeneration(t *testing.T) {
	store := state.NewMemStore()
	_, app, deps := seedWorkerScopes(t, store, api.PlanPro)
	old := seedScopedWorker(t, store, app, deps["default"])
	neighbor := seedScopedWorker(t, store, app, deps["staging"])
	newDep, err := store.CreateDeployment(context.Background(), state.Deployment{AppID: app.ID, Scope: "default",
		Kind: state.DeploymentKindImage, ImageDigest: "sha256:new-generation", Status: state.DeployLive})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateDeploymentStatus(context.Background(), deps["default"].ID, state.DeploySuperseded, ""); err != nil {
		t.Fatal(err)
	}
	latest := seedScopedWorker(t, store, app, newDep)
	vmm := &recordingStopVMM{fakeVMM: &fakeVMM{}}
	engine := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")
	if err := engine.SeedLedger(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := engine.ReconcileWorkerPoolForScope(context.Background(), app.ID, "default", 1, TriggerWorkerPool); err != nil {
		t.Fatal(err)
	}
	engine.WaitWorkerStops()
	for id, want := range map[string]string{old.ID: string(state.StateStopped), latest.ID: string(state.StateRunning), neighbor.ID: string(state.StateRunning)} {
		got, err := store.InstanceByID(context.Background(), id)
		if err != nil || got.State != want {
			t.Fatalf("generation cleanup state = %q, want %q, err=%v", got.State, want, err)
		}
	}
	if vmm.stopInstanceOnNodeN != 1 || vmm.coldBoots != 0 {
		t.Fatalf("generation cleanup stopped=%d booted=%d", vmm.stopInstanceOnNodeN, vmm.coldBoots)
	}
}

func TestWorkerScopedPoolHoldsDuringMigration(t *testing.T) {
	store := state.NewMemStore()
	_, app, deps := seedWorkerScopes(t, store, api.PlanPro)
	ins := seedScopedWorker(t, store, app, deps["default"])
	neighbor := seedScopedWorker(t, store, app, deps["staging"])
	if err := store.UpdateInstanceState(context.Background(), ins.ID, string(state.StateMigrating)); err != nil {
		t.Fatal(err)
	}
	vmm := &recordingStopVMM{fakeVMM: &fakeVMM{}}
	engine := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")
	if err := engine.ReconcileWorkerPools(context.Background(), app.ID, TriggerWorkerPool); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("migration granted pool teardown: %v", err)
	}
	engine.WaitWorkerStops()
	if vmm.destroys != 0 || vmm.stopInstanceOnNodeN != 0 || vmm.coldBoots != 0 {
		t.Fatal("migration hold mutated a VM")
	}
	// An explicit neighboring scope does not acquire migration ownership.
	if err := engine.ReconcileWorkerPoolForScope(context.Background(), app.ID, "staging", 0, TriggerWorkerPool); err != nil {
		t.Fatal(err)
	}
	engine.WaitWorkerStops()
	got, _ := store.InstanceByID(context.Background(), ins.ID)
	other, _ := store.InstanceByID(context.Background(), neighbor.ID)
	if got.State != string(state.StateMigrating) || other.State != string(state.StateStopped) {
		t.Fatalf("scoped teardown interfered with migration: %s/%s", got.State, other.State)
	}
}

func TestWorkerScopedPoolHonorsManifestReplicaBounds(t *testing.T) {
	store := state.NewMemStore()
	_, app, _ := seedWorkerScopes(t, store, api.PlanScale)
	manifest := app.Manifest
	manifest.WorkerReplicas = &state.WorkerScaling{Min: 2, Max: 2}
	if _, err := store.UpdateApp(context.Background(), app.ID, state.UpdateAppParams{Manifest: &manifest}); err != nil {
		t.Fatal(err)
	}
	engine := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0")
	for _, desired := range []int{10, 0} {
		if err := engine.ReconcileWorkerPoolForScope(context.Background(), app.ID, "default", desired, TriggerWorkerPool); err != nil {
			t.Fatal(err)
		}
		engine.WaitWorkerStops()
		counts := scopedWorkerCounts(t, store, app.ID)
		if counts["default"] != 2 || counts["staging"] != 0 {
			t.Fatalf("replica bounds or environment ignored: %v", counts)
		}
	}
}

func TestWorkerScopedDemandKeepsBindingCapsIndependent(t *testing.T) {
	store := state.NewMemStore()
	acct, app, _ := seedWorkerScopes(t, store, api.PlanScale)
	for _, binding := range []state.QueueBinding{
		{AccountID: acct.ID, AppID: app.ID, Name: "orders", QueueName: "orders", Mode: "pull", WorkloadClass: state.WorkloadClassWorker, Enabled: true, MaxConcurrency: 1},
		{AccountID: acct.ID, AppID: app.ID, Name: "payments", QueueName: "payments", Mode: "pull", WorkloadClass: state.WorkloadClassWorker, Enabled: true, MaxConcurrency: 4},
		{AccountID: acct.ID, AppID: app.ID, Name: "disabled", QueueName: "disabled", Mode: "pull", WorkloadClass: state.WorkloadClassWorker, Enabled: false, MaxConcurrency: 5},
	} {
		if _, err := store.CreateQueueBindingWithConsumer(context.Background(), binding); err != nil {
			t.Fatal(err)
		}
	}
	enqueueScopedDemand(t, store, acct, app, "default", "orders", 40)
	enqueueScopedDemand(t, store, acct, app, "default", "payments", 11)
	enqueueScopedDemand(t, store, acct, app, "default", "disabled", 30)
	enqueueScopedDemand(t, store, acct, app, "staging", "orders", 1)
	engine := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0")
	if err := engine.ReconcileWorkerPools(context.Background(), app.ID, TriggerWorkerPool); err != nil {
		t.Fatal(err)
	}
	engine.WaitWorkerStops()
	counts := scopedWorkerCounts(t, store, app.ID)
	if counts["default"] != 3 || counts["staging"] != 1 {
		t.Fatalf("scoped binding demand = %v; want default=3 staging=1", counts)
	}
}

func TestWorkerScopedDemandRetirementHoldsBacklogWithoutAdmittingWorkers(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct, app, deps := seedWorkerScopes(t, store, api.PlanPro)
	for _, dep := range deps {
		seedScopedWorker(t, store, app, dep)
	}
	binding, err := store.CreateQueueBindingWithConsumer(ctx, state.QueueBinding{AccountID: acct.ID, AppID: app.ID,
		Name: "orders", QueueName: "orders", Mode: "pull", WorkloadClass: state.WorkloadClassWorker, Enabled: true, MaxConcurrency: 5})
	if err != nil {
		t.Fatal(err)
	}
	var leased state.Invocation
	for _, scope := range []string{"default", "staging"} {
		rows := enqueueScopedDemand(t, store, acct, app, scope, "orders", 50)
		if scope == "default" {
			leased, err = store.ClaimInvocationWithCap(ctx, rows[0].ID, "", 60, 10)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	// A historical label must not hide a live lease after rename and retirement.
	name := "payments"
	if _, err := store.UpdateQueueBindingWithConsumer(ctx, acct.ID, app.ID, binding.Binding.ID, state.UpdateQueueBindingParams{QueueName: &name}); err != nil {
		t.Fatal(err)
	}

	if _, err := store.DeleteQueueBindingWithConsumer(ctx, acct.ID, app.ID, binding.Binding.ID); err != nil {
		t.Fatal(err)
	}
	vmm := &recordingStopVMM{fakeVMM: &fakeVMM{}}
	engine := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")
	if err := engine.SeedLedger(ctx); err != nil {
		t.Fatal(err)
	}
	if err := engine.ReconcileWorkerPools(ctx, app.ID, TriggerWorkerPool); err != nil {
		t.Fatal(err)
	}
	engine.WaitWorkerStops()
	counts := scopedWorkerCounts(t, store, app.ID)
	if counts["default"] != 1 || counts["staging"] != 0 || vmm.stopInstanceOnNodeN != 1 || vmm.coldBoots != 0 {
		t.Fatalf("retirement changed leased scope or admitted workers: counts=%v stops=%d boots=%d", counts, vmm.stopInstanceOnNodeN, vmm.coldBoots)
	}
	if err := store.CompleteInvocation(ctx, leased.ID, nil); err != nil {
		t.Fatal(err)
	}
	if err := engine.ReconcileWorkerPools(ctx, app.ID, TriggerWorkerPool); err != nil {
		t.Fatal(err)
	}
	engine.WaitWorkerStops()
	counts = scopedWorkerCounts(t, store, app.ID)
	if counts["default"] != 0 || counts["staging"] != 0 || vmm.stopInstanceOnNodeN != 2 || vmm.coldBoots != 0 {
		t.Fatalf("completed lease did not release retirement hold: counts=%v stops=%d boots=%d", counts, vmm.stopInstanceOnNodeN, vmm.coldBoots)
	}
	for _, scope := range []string{"default", "staging"} {
		stats, err := store.QueueStateForBindingInScope(ctx, app.ID, binding.Binding.ID, scope)
		wantDepth := 50
		if scope == "default" {
			wantDepth--
		}
		if err != nil || stats.Depth != wantDepth {
			t.Fatalf("retirement hid or removed backlog: scope=%s depth=%d err=%v", scope, stats.Depth, err)
		}
	}
}

type workerDemandFailureStore struct {
	state.Store
	failScope string
}

func (s *workerDemandFailureStore) QueueStateInScope(ctx context.Context, appID, scope string) (state.QueueStats, error) {
	if scope == s.failScope {
		return state.QueueStats{}, errors.New("queue observation unavailable")
	}
	return s.Store.QueueStateInScope(ctx, appID, scope)
}

func TestWorkerScopedDemandReadFailurePreservesAllPools(t *testing.T) {
	ctx := context.Background()
	store := &workerDemandFailureStore{Store: state.NewMemStore(), failScope: "staging"}
	_, app, deps := seedWorkerScopes(t, store, api.PlanPro)
	seedScopedWorker(t, store, app, deps["default"])
	seedScopedWorker(t, store, app, deps["staging"])
	vmm := &recordingStopVMM{fakeVMM: &fakeVMM{}}
	engine := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")
	if err := engine.ReconcileWorkerPools(ctx, app.ID, TriggerWorkerPool); err == nil {
		t.Fatal("missing demand was treated as an empty queue")
	}
	engine.WaitWorkerStops()
	counts := scopedWorkerCounts(t, store, app.ID)
	if counts["default"] != 1 || counts["staging"] != 1 || vmm.stopInstanceOnNodeN != 0 || vmm.coldBoots != 0 {
		t.Fatalf("read failure mutated fleet: %v, stops=%d boots=%d", counts, vmm.stopInstanceOnNodeN, vmm.coldBoots)
	}
}

func TestWorkerScopedPoolRejectsUnqualifiedScope(t *testing.T) {
	store := state.NewMemStore()
	_, app, deps := seedWorkerScopes(t, store, api.PlanPro)
	seedScopedWorker(t, store, app, deps["default"])
	vmm := &recordingStopVMM{fakeVMM: &fakeVMM{}}
	engine := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")
	for _, scope := range []string{"", "__all__", "UPPER", "staging/other"} {
		if err := engine.ReconcileWorkerPoolForScope(context.Background(), app.ID, scope, 0, ""); !errors.Is(err, state.ErrInvalidArgument) {
			t.Fatalf("scope %q accepted: %v", scope, err)
		}
		engine.WaitWorkerStops()
	}
	if err := engine.ReconcileWorkerPoolForScope(context.Background(), app.ID, "default", -1, ""); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatal(err)
	}
	engine.WaitWorkerStops()
	if vmm.stopInstanceOnNodeN != 0 {
		t.Fatal("invalid selector stopped workers")
	}
}

type workerScopeLag struct{ lag int64 }

func (r workerScopeLag) BrokerLag(context.Context, string) (int64, bool, error) {
	return r.lag, true, nil
}

func TestWorkerScopedDemandArbitratesTargetsAndDoesNotBroadcastLag(t *testing.T) {
	store := state.NewMemStore()
	acct, app, _ := seedWorkerScopes(t, store, api.PlanScale)
	policy := &state.ScalingPolicy{Targets: []state.ScalingTarget{
		{Metric: api.ScalingMetricQueueDepth, Value: 10}, {Metric: api.ScalingMetricQueueLag, Value: 100},
	}}
	app, err := store.UpdateApp(context.Background(), app.ID, state.UpdateAppParams{ScalingPolicy: policy, SetScalingPolicy: true})
	if err != nil {
		t.Fatal(err)
	}
	enqueueScopedDemand(t, store, acct, app, "default", "", 21)
	enqueueScopedDemand(t, store, acct, app, "staging", "", 1)
	ops := wire.NewOpsMetrics("schedd")
	engine := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0").WithBrokerLagReader(workerScopeLag{lag: 400}).WithOpsMetrics(ops)
	if err := engine.ReconcileWorkerPools(context.Background(), app.ID, TriggerWorkerPool); err != nil {
		t.Fatal(err)
	}
	engine.WaitWorkerStops()
	counts := scopedWorkerCounts(t, store, app.ID)
	if counts["default"] != 4 || counts["staging"] != 1 {
		t.Fatalf("scoped arbitration = %v; want default=4 staging=1", counts)
	}
	for _, metric := range []string{api.ScalingMetricQueueDepth, api.ScalingMetricQueueLag} {
		if got := scopedWorkerCounter(t, ops, "schedd_scale_up_winning_signal_total", map[string]string{"app": app.ID, "metric": metric}); got != 1 {
			t.Fatalf("winner %s = %v; want one scoped admission decision", metric, got)
		}
	}
	if got := scopedWorkerCounter(t, ops, "schedd_scale_up_decisions_total", map[string]string{"app": app.ID, "outcome": "admit"}); got != 2 {
		t.Fatalf("admission decisions = %v; want two", got)
	}
	if err := engine.ReconcileWorkerPools(context.Background(), app.ID, TriggerWorkerPool); err != nil {
		t.Fatal(err)
	}
	engine.WaitWorkerStops()
	if got := scopedWorkerCounter(t, ops, "schedd_scale_up_decisions_total", map[string]string{"app": app.ID, "outcome": "admit"}); got != 2 {
		t.Fatalf("unchanged fleet reported another admission: %v", got)
	}
}

func scopedWorkerCounter(t *testing.T, ops *wire.OpsMetrics, name string, labels map[string]string) float64 {
	t.Helper()
	families, err := ops.Registry().Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		if family.GetName() != name {
			continue
		}
		for _, sample := range family.GetMetric() {
			matched := 0
			for _, label := range sample.GetLabel() {
				if want, ok := labels[label.GetName()]; ok && want == label.GetValue() {
					matched++
				}
			}
			if matched == len(labels) {
				return sample.GetCounter().GetValue()
			}
		}
	}
	return 0
}

func TestWorkerScopedDemandUnknownLagPreservesFleet(t *testing.T) {
	store := state.NewMemStore()
	_, app, deps := seedWorkerScopes(t, store, api.PlanPro)
	policy := &state.ScalingPolicy{Target: &state.ScalingTarget{Metric: api.ScalingMetricQueueLag, Value: 100}}
	if _, err := store.UpdateApp(context.Background(), app.ID, state.UpdateAppParams{ScalingPolicy: policy, SetScalingPolicy: true}); err != nil {
		t.Fatal(err)
	}
	seedScopedWorker(t, store, app, deps["staging"])
	vmm := &recordingStopVMM{fakeVMM: &fakeVMM{}}
	engine := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0").WithBrokerLagReader(workerScopeLag{lag: 0})
	if err := engine.ReconcileWorkerPools(context.Background(), app.ID, TriggerWorkerPool); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("unscoped lag authorized environment mutation: %v", err)
	}
	engine.WaitWorkerStops()
	if scopedWorkerCounts(t, store, app.ID)["staging"] != 1 || vmm.stopInstanceOnNodeN != 0 {
		t.Fatal("missing lag removed neighboring worker")
	}
}

func TestWorkerCustomMetricScalesOnFreshAppBacklog(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "mcp-worker-scale@example.test", api.PlanScale)
	if err != nil {
		t.Fatal(err)
	}
	metricName := "mcp_tasks_outstanding"
	target := 2.0
	app, err := store.CreateApp(ctx, state.App{
		AccountID: account.ID, Slug: "mcp-worker-scale", Type: state.AppTypeApp,
		WorkloadClass: state.WorkloadClassWorker, Status: state.AppActive, RAMMB: 256, MaxConcurrency: 10,
		Manifest: state.AppManifest{
			ExecutionMode:  api.ExecutionModeWorker,
			WorkerReplicas: &state.WorkerScaling{Min: 1, Max: 10, Metric: api.ScalingMetricCustom, Name: metricName, Target: target},
		},
		ScalingPolicy: &state.ScalingPolicy{MinInstances: 1, MaxInstances: 10, Target: &state.ScalingTarget{Metric: api.ScalingMetricCustom, Name: metricName, Value: target}},
	})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:mcp-worker", Status: state.DeployLive})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateDeploymentStatus(ctx, dep.ID, state.DeployLive, ""); err != nil {
		t.Fatal(err)
	}
	engine := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0")
	if err := engine.ReconcileWorkerPools(ctx, app.ID, TriggerWorkerPool); err != nil {
		t.Fatalf("bootstrap worker before first custom metric: %v", err)
	}
	engine.WaitWorkerStops()
	if got := scopedWorkerCounts(t, store, app.ID)["default"]; got != 1 {
		t.Fatalf("missing initial metric should start the configured minimum of 1 worker, got %d", got)
	}
	if err := store.PutCustomMetric(ctx, app.ID, metricName, 10, time.Now(), api.MaxCustomMetricsPerApp); err != nil {
		t.Fatal(err)
	}
	if err := engine.ReconcileWorkerPools(ctx, app.ID, TriggerWorkerPool); err != nil {
		t.Fatal(err)
	}
	engine.WaitWorkerStops()
	if got := scopedWorkerCounts(t, store, app.ID)["default"]; got != 5 {
		t.Fatalf("fresh custom backlog desired ceil(10/2)=5 replicas, got %d", got)
	}
	if err := store.PutCustomMetric(ctx, app.ID, metricName, 0, time.Now(), api.MaxCustomMetricsPerApp); err != nil {
		t.Fatal(err)
	}
	if err := engine.ReconcileWorkerPools(ctx, app.ID, TriggerWorkerPool); err != nil {
		t.Fatal(err)
	}
	engine.WaitWorkerStops()
	if got := scopedWorkerCounts(t, store, app.ID)["default"]; got != 1 {
		t.Fatalf("empty custom backlog should return to the minimum of 1 replica, got %d", got)
	}
}

func TestWorkerCustomMetricMissingOrStalePreservesFleet(t *testing.T) {
	for _, stale := range []bool{false, true} {
		name := "missing"
		if stale {
			name = "stale"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			store := state.NewMemStore()
			account, err := store.CreateAccount(ctx, "mcp-worker-"+name+"@example.test", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			metricName := "mcp_tasks_outstanding"
			target := 2.0
			app, err := store.CreateApp(ctx, state.App{
				AccountID: account.ID, Slug: "mcp-worker-" + name, Type: state.AppTypeApp,
				WorkloadClass: state.WorkloadClassWorker, Status: state.AppActive, RAMMB: 256, MaxConcurrency: 5,
				Manifest: state.AppManifest{ExecutionMode: api.ExecutionModeWorker,
					WorkerReplicas: &state.WorkerScaling{Min: 0, Max: 5, Metric: api.ScalingMetricCustom, Name: metricName, Target: target}},
				ScalingPolicy: &state.ScalingPolicy{Target: &state.ScalingTarget{Metric: api.ScalingMetricCustom, Name: metricName, Value: target}},
			})
			if err != nil {
				t.Fatal(err)
			}
			dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:mcp-worker-" + name, Status: state.DeployLive})
			if err != nil {
				t.Fatal(err)
			}
			if err := store.UpdateDeploymentStatus(ctx, dep.ID, state.DeployLive, ""); err != nil {
				t.Fatal(err)
			}
			seedScopedWorker(t, store, app, dep)
			if stale {
				observedAt := time.Now().Add(-time.Duration(api.CustomMetricFreshnessSeconds+1) * time.Second)
				if err := store.PutCustomMetric(ctx, app.ID, metricName, 0, observedAt, api.MaxCustomMetricsPerApp); err != nil {
					t.Fatal(err)
				}
			}
			engine := newEngine(t, store, &recordingStopVMM{fakeVMM: &fakeVMM{}}, &fakeNotifier{}, "1.10.0")
			if err := engine.ReconcileWorkerPools(ctx, app.ID, TriggerWorkerPool); !errors.Is(err, state.ErrConflict) {
				t.Fatalf("missing custom metric error = %v, want conflict", err)
			}
			engine.WaitWorkerStops()
			if got := scopedWorkerCounts(t, store, app.ID)["default"]; got != 1 {
				t.Fatalf("missing/stale custom metric changed worker fleet to %d replicas", got)
			}
		})
	}
}

// adr: 590 — each deployed environment owns its worker runtime policy.
func TestWorkerReconcileHonorsPinnedStageReplicas(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "regression-worker-pin@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "regression-worker-pin"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "regression-worker-pin-app", Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 5, WorkloadClass: state.WorkloadClassWorker, Status: state.AppActive, Manifest: state.AppManifest{ExecutionMode: api.ExecutionModeWorker, WorkerReplicas: &state.WorkerScaling{Min: 0, Max: 5}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: "staging"}); err != nil {
		t.Fatal(err)
	}
	settings, err := state.WorkloadSettingsFromApp(app)
	if err != nil {
		t.Fatal(err)
	}
	settings.Manifest.WorkerReplicas = &state.WorkerScaling{Min: 2, Max: 2}
	spec, err := store.PutProjectEnvironmentWorkloadSpec(ctx, account.ID, project.ID, "staging", app.ID, 0, settings)
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "staging", Status: state.DeployPending})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatal(err)
	}
	production, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Status: state.DeployLive})
	if err != nil {
		t.Fatal(err)
	}
	seedScopedWorker(t, store, app, production)
	seedScopedWorker(t, store, app, dep)
	seedScopedWorker(t, store, app, dep)
	settings.Manifest.WorkerReplicas = &state.WorkerScaling{Min: 0, Max: 1}
	if _, err := store.PutProjectEnvironmentWorkloadSpec(ctx, account.ID, project.ID, "staging", app.ID, spec.Revision, settings); err != nil {
		t.Fatal(err)
	}
	vmm := &recordingStopVMM{fakeVMM: &fakeVMM{}}
	engine := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")
	if err := engine.SeedLedger(ctx); err != nil {
		t.Fatal(err)
	}
	if err := engine.ReconcileWorkerPools(ctx, app.ID, TriggerWorkerPool); err != nil {
		t.Fatal(err)
	}
	engine.WaitWorkerStops()
	counts := scopedWorkerCounts(t, store, app.ID)
	if counts["staging"] != 2 || counts["default"] != 0 || vmm.stopInstanceOnNodeN != 1 {
		t.Fatalf("deployed pool settings: counts=%v stops=%d", counts, vmm.stopInstanceOnNodeN)
	}
	// A later production mode change cannot drain the pinned worker stage.
	mode, class := api.ExecutionModeService, state.WorkloadClassHTTP
	if _, err := store.UpdateApp(ctx, app.ID, state.UpdateAppParams{Manifest: &state.AppManifest{ExecutionMode: mode}, WorkloadClass: &class}); err != nil {
		t.Fatal(err)
	}
	if err := engine.ReconcileWorkerPools(ctx, app.ID, TriggerWorkerPool); err != nil {
		t.Fatal(err)
	}
	engine.WaitWorkerStops()
	if counts := scopedWorkerCounts(t, store, app.ID); counts["staging"] != 2 || vmm.stopInstanceOnNodeN != 1 {
		t.Fatalf("production mode changed stage pool: counts=%v stops=%d", counts, vmm.stopInstanceOnNodeN)
	}
}
