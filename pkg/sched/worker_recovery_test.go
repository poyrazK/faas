// adr: 20261017221000
package sched

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

func workerRecoveryFixture(t *testing.T, store state.Store, vmm *fakeVMM, desired int) (*Engine, state.App, state.Deployment) {
	t.Helper()
	ctx := context.Background()
	_, app, dep := seedApp(t, store, api.PlanPro, 128, 5)
	if err := store.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatal(err)
	}
	dep, err := store.DeploymentByID(ctx, dep.ID)
	if err != nil {
		t.Fatal(err)
	}
	manifest := app.Manifest
	manifest.ExecutionMode = api.ExecutionModeWorker
	manifest.WorkerReplicas = &state.WorkerScaling{Min: desired}
	app, err = store.UpdateApp(ctx, app.ID, state.UpdateAppParams{Manifest: &manifest})
	if err != nil {
		t.Fatal(err)
	}
	e := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")
	e.workerReconcileSubmit = func(context.Context, string) {}
	return e, app, dep
}

func assertRecoveryWorkers(t *testing.T, store state.Store, appID string, want int) {
	t.Helper()
	rows, err := store.ListInstancesForApp(context.Background(), appID)
	if err != nil {
		t.Fatal(err)
	}
	ready, managed := 0, 0
	for _, ins := range rows {
		if ins.Mode != string(state.InstanceModeWorker) {
			continue
		}
		if ins.State == string(state.StateRunning) {
			ready++
		}
		if state.State(ins.State).CountsForRAM() {
			managed++
		}
	}
	if ready != want || managed != want {
		t.Fatalf("workers ready=%d managed=%d, want %d", ready, managed, want)
	}
}

// Refusal creates no instance and no future event. A new scheduler/store client
// must retain the deadline, then recover the floor solely through due sweeps.
func exerciseWorkerRecoveryRestart(t *testing.T, store state.Store, reopen func() state.Store) {
	t.Helper()
	ctx := context.Background()
	vmm := &fakeVMM{}
	e, app, _ := workerRecoveryFixture(t, store, vmm, 2)
	var clock atomic.Int64
	clock.Store(time.Now().UTC().Truncate(time.Microsecond).UnixNano())
	now := func() time.Time { return time.Unix(0, clock.Load()).UTC() }
	e.now = now
	node, err := store.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
	if err != nil {
		t.Fatal(err)
	}
	node.AdmissionCeilingMB = 1
	if _, err := store.UpsertComputeNodeFromOperator(ctx, node); err != nil {
		t.Fatal(err)
	}
	loop := NewLoop(nil, e, testLog()).WithClock(now)
	loop.dispatchWorkerRecovery(ctx)
	loop.workPool().drain()
	assertRecoveryWorkers(t, store, app.ID, 0)
	rows, err := store.ListInstancesForApp(ctx, app.ID)
	if err != nil || len(rows) != 0 {
		t.Fatalf("refused admission created instances: %d %v", len(rows), err)
	}
	retry, err := store.WorkerRecoveryByApp(ctx, app.ID)
	if err != nil || retry.Status != "waiting_capacity" || retry.Failures != 1 || !retry.NextAttemptAt.Equal(now().Add(5*time.Second)) {
		t.Fatalf("capacity refusal was not durable: %+v %v", retry, err)
	}
	if len(retry.Pools) != 1 || retry.Pools[0].Desired != 2 || retry.Pools[0].Running != 0 || !retry.Pools[0].ObservedAt.Equal(now()) {
		t.Fatalf("capacity refusal lost pool diagnostics: %+v", retry.Pools)
	}
	if summary, err := store.WorkerRecoverySummary(ctx, "", now()); err != nil || summary.Missing != 2 || summary.Unobserved != 0 {
		t.Fatalf("capacity shortage invisible to alerts: %+v %v", summary, err)
	}
	store = reopen()
	e = newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")
	e.now = now
	loop = NewLoop(nil, e, testLog()).WithClock(now)
	node.AdmissionCeilingMB = 4096
	if _, err := store.UpsertComputeNodeFromOperator(ctx, node); err != nil {
		t.Fatal(err)
	}
	loop.dispatchWorkerRecovery(ctx)
	loop.workPool().drain()
	for range 3 {
		e.ReconcileWorkerApp(ctx, app.ID)
	}
	assertRecoveryWorkers(t, store, app.ID, 0)
	held, err := store.WorkerRecoveryByApp(ctx, app.ID)
	if err != nil || held.Failures != retry.Failures || !held.NextAttemptAt.Equal(retry.NextAttemptAt) {
		t.Fatalf("events changed cooldown: %+v %v", held, err)
	}
	clock.Store(retry.NextAttemptAt.UnixNano())
	loop.dispatchWorkerRecovery(ctx)
	loop.workPool().drain()
	assertRecoveryWorkers(t, store, app.ID, 2)
	retry, err = store.WorkerRecoveryByApp(ctx, app.ID)
	if err != nil || retry.Status != "ready" || retry.Failures != 0 || !retry.NextAttemptAt.Equal(now().Add(30*time.Second)) {
		t.Fatalf("pool did not become healthy: %+v %v", retry, err)
	}
	if len(retry.Pools) != 1 || retry.Pools[0].Running != 2 || !retry.Pools[0].ObservedAt.Equal(now()) {
		t.Fatalf("recovered pool diagnostics stayed stale: %+v", retry.Pools)
	}
	if summary, err := store.WorkerRecoverySummary(ctx, "", now()); err != nil || summary.Missing != 0 || summary.Running != 2 || summary.Overdue != 0 {
		t.Fatalf("recovery did not clear alerts: %+v %v", summary, err)
	}
	for range 3 {
		clock.Add(int64(30 * time.Second))
		loop.dispatchWorkerRecovery(ctx)
		loop.workPool().drain()
		assertRecoveryWorkers(t, store, app.ID, 2)
	}
	vmm.mu.Lock()
	defer vmm.mu.Unlock()
	if vmm.coldBoots != 2 {
		t.Fatalf("boots=%d, want exactly two", vmm.coldBoots)
	}
}

func TestWorkerRecoveryRestartWithoutEvents(t *testing.T) {
	store := state.NewMemStore()
	exerciseWorkerRecoveryRestart(t, store, func() state.Store { return store })
}

func TestWorkerRecoveryBootFailuresSurviveRestart(t *testing.T) {
	store := state.NewMemStore()
	ctx := context.Background()
	vmm := &fakeVMM{wakeErr: errors.New("vmmd unavailable")}
	e, app, _ := workerRecoveryFixture(t, store, vmm, 1)
	now := time.Now().UTC()
	for i, delay := range []time.Duration{5 * time.Second, 10 * time.Second, 20 * time.Second} {
		e.now = func() time.Time { return now }
		e.ReconcileWorkerApp(ctx, app.ID)
		retry, err := store.WorkerRecoveryByApp(ctx, app.ID)
		if err != nil || retry.Status != "retrying_startup" || retry.Failures != i+1 || !retry.NextAttemptAt.Equal(now.Add(delay)) {
			t.Fatalf("attempt %d: %+v %v", i, retry, err)
		}
		now = retry.NextAttemptAt
		e = newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")
		e.workerReconcileSubmit = func(context.Context, string) {}
	}
	if delay := workerRecoveryBackoff(api.WorkerRecoveryFailureCountMax); delay != 5*time.Minute {
		t.Fatalf("backoff cap=%v", delay)
	}
	// A fixed deployment/configuration should be actionable before cooldown.
	now = now.Add(-time.Second)
	manifest := app.Manifest
	manifest.Env = map[string]string{"FIXED": "1"}
	if _, err := store.UpdateApp(ctx, app.ID, state.UpdateAppParams{Manifest: &manifest}); err != nil {
		t.Fatal(err)
	}
	vmm.mu.Lock()
	vmm.wakeErr = nil
	vmm.mu.Unlock()
	e.now = func() time.Time { return now }
	e.ReconcileWorkerApp(ctx, app.ID)
	assertRecoveryWorkers(t, store, app.ID, 1)
	retry, err := store.WorkerRecoveryByApp(ctx, app.ID)
	if err != nil || retry.Failures != 0 || retry.Status != "ready" {
		t.Fatalf("fixed intent still blocked: %+v %v", retry, err)
	}
}

func TestWorkerRecoveryConcurrentSchedulers(t *testing.T) {
	store := state.NewMemStore()
	vmm := &fakeVMM{bootStarted: make(chan struct{}, 1), bootRelease: make(chan struct{})}
	e, app, _ := workerRecoveryFixture(t, store, vmm, 1)
	peer := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")
	peer.workerReconcileSubmit = func(context.Context, string) {}
	done := make(chan struct{})
	go func() { e.ReconcileWorkerApp(context.Background(), app.ID); close(done) }()
	defer func() { close(vmm.bootRelease); <-done }()
	select {
	case <-vmm.bootStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("first engine did not boot")
	}
	peer.ReconcileWorkerApp(context.Background(), app.ID)
	rows, err := store.ListInstancesForApp(context.Background(), app.ID)
	if err != nil || len(rows) != 1 {
		t.Fatalf("peer admitted a duplicate: %d %v", len(rows), err)
	}
}

func TestWorkerRecoveryExpiredLeaseResumesAfterRestart(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	e, app, _ := workerRecoveryFixture(t, store, &fakeVMM{}, 1)
	now := time.Now().UTC()
	deps, err := store.LiveDeployments(ctx, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	lease, claimed, err := store.ClaimWorkerRecovery(ctx, app.ID, "", workerRecoveryRevision(app, deps), uuid.NewString(), now, now.Add(time.Minute))
	if err != nil || !claimed {
		t.Fatalf("claim: %+v %v", lease, err)
	}
	e.now = func() time.Time { return now }
	loop := NewLoop(nil, e, testLog()).WithClock(e.now)
	loop.dispatchWorkerRecovery(ctx)
	loop.workPool().drain()
	assertRecoveryWorkers(t, store, app.ID, 0)
	now = now.Add(time.Minute)
	loop.dispatchWorkerRecovery(ctx)
	loop.workPool().drain()
	assertRecoveryWorkers(t, store, app.ID, 1)
	lease.Status, lease.UpdatedAt, lease.NextAttemptAt = "waiting_capacity", now, now.Add(5*time.Second)
	if err := store.CompleteWorkerRecovery(ctx, lease.ClaimToken, lease); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("stale scheduler overwrote recovery: %v", err)
	}
}

func TestWorkerRecoverySaturatedPoolLeavesAppsDue(t *testing.T) {
	store := state.NewMemStore()
	e, app, _ := workerRecoveryFixture(t, store, &fakeVMM{}, 1)
	loop := NewLoop(nil, e, testLog())
	release := fillSlots(t, loop.workPool(), workWorkerRecovery)
	defer release()
	loop.runWorkerRecovery(context.Background())
	if _, err := store.WorkerRecoveryByApp(context.Background(), app.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("dropped app was claimed: %v", err)
	}
	release()
	loop.workPool().drain()
	loop.dispatchWorkerRecovery(context.Background())
	loop.workPool().drain()
	assertRecoveryWorkers(t, store, app.ID, 1)
}

func TestWorkerRecoveryCooldownAllowsScopedScaleIn(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	account, app, _ := seedWorkerScopes(t, store, api.PlanPro)
	defaultDemand := enqueueScopedDemand(t, store, account, app, "default", "", 1)
	stagingDemand := enqueueScopedDemand(t, store, account, app, "staging", "", 1)
	vmm := &fakeVMM{}
	e := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")
	e.workerReconcileSubmit = func(context.Context, string) {}
	now := time.Now().UTC()
	e.now = func() time.Time { return now }
	node, err := store.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
	if err != nil {
		t.Fatal(err)
	}
	node.AdmissionCeilingMB = app.RAMMB + api.PerVMOverheadMB
	if _, err := store.UpsertComputeNodeFromOperator(ctx, node); err != nil {
		t.Fatal(err)
	}
	if err := e.ReconcileWorkerPools(ctx, app.ID, TriggerWorkerPool); err != nil {
		t.Fatal(err)
	}
	assertRecoveryWorkers(t, store, app.ID, 1)
	retry, err := store.WorkerRecoveryByApp(ctx, app.ID)
	if err != nil || retry.Status != "waiting_capacity" {
		t.Fatalf("missing cooldown: %+v %v", retry, err)
	}
	for _, row := range defaultDemand {
		if err := store.CancelInvocation(ctx, row.ID); err != nil {
			t.Fatal(err)
		}
	}
	// A canceled environment can stop even though a neighboring pool failed.
	if err := e.ReconcileWorkerPools(ctx, app.ID, TriggerWorkerPool); err != nil {
		t.Fatal(err)
	}
	e.WaitWorkerStops()
	assertRecoveryWorkers(t, store, app.ID, 0)
	if err := e.ReconcileWorkerPoolForScope(ctx, app.ID, "default", 0, TriggerWorkerPool); err != nil {
		t.Fatal(err)
	}
	held, err := store.WorkerRecoveryByApp(ctx, app.ID)
	if err != nil || held.Failures != retry.Failures || !held.NextAttemptAt.Equal(retry.NextAttemptAt) {
		t.Fatalf("scale-in changed cooldown: %+v %v", held, err)
	}
	if len(held.Pools) != 2 || held.Pools[0].Scope != "default" || held.Pools[0].Desired != 0 ||
		held.Pools[1].Scope != "staging" || held.Pools[1].Desired != 1 {
		t.Fatalf("scoped observation erased neighboring demand: %+v", held.Pools)
	}
	// Canceling the remaining demand keeps the zero floor at zero on recovery.
	for _, row := range stagingDemand {
		if err := store.CancelInvocation(ctx, row.ID); err != nil {
			t.Fatal(err)
		}
	}
	now = retry.NextAttemptAt
	loop := NewLoop(nil, e, testLog()).WithClock(e.now)
	loop.dispatchWorkerRecovery(ctx)
	loop.workPool().drain()
	assertRecoveryWorkers(t, store, app.ID, 0)
	ready, err := store.WorkerRecoveryByApp(ctx, app.ID)
	if err != nil || ready.Status != "ready" || ready.Failures != 0 {
		t.Fatalf("zero floor did not settle: %+v %v", ready, err)
	}
	if summary, err := store.WorkerRecoverySummary(ctx, "", now); err != nil || summary.Missing != 0 || summary.Desired != 0 || summary.Unobserved != 0 {
		t.Fatalf("intentional scale-to-zero retained an alert: %+v %v", summary, err)
	}
	if vmm.coldBoots != 1 {
		t.Fatalf("zero floor restarted a worker: %d", vmm.coldBoots)
	}
}

func TestWorkerRecoveryDemandReadFailureIsDurable(t *testing.T) {
	ctx := context.Background()
	fault := &workerDemandFailureStore{Store: state.NewMemStore(), failScope: "staging"}
	store := statetest.NewStore(fault)
	account, app, _ := seedWorkerScopes(t, store, api.PlanPro)
	for _, scope := range []string{"default", "staging"} {
		enqueueScopedDemand(t, store, account, app, scope, "", 1)
	}
	e := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0")
	e.workerReconcileSubmit = func(context.Context, string) {}
	now := time.Now().UTC()
	e.now = func() time.Time { return now }
	if err := e.ReconcileWorkerPools(ctx, app.ID, TriggerWorkerPool); err == nil {
		t.Fatal("missing demand was accepted")
	}
	retry, err := store.WorkerRecoveryByApp(ctx, app.ID)
	if err != nil || retry.Status != "waiting_dependency" || retry.Failures != 1 {
		t.Fatalf("read failure not persisted: %+v %v", retry, err)
	}
	fault.failScope = ""
	e.ReconcileWorkerApp(ctx, app.ID)
	assertRecoveryWorkers(t, store, app.ID, 0)
	now = retry.NextAttemptAt
	loop := NewLoop(nil, e, testLog()).WithClock(e.now)
	loop.dispatchWorkerRecovery(ctx)
	loop.workPool().drain()
	assertRecoveryWorkers(t, store, app.ID, 2)
}

// adr: 20261017223000 — a mode switch must retire old diagnostic pools too.
func TestWorkerRecoveryDropsRetiredPoolObservations(t *testing.T) {
	store := state.NewMemStore()
	e, app, _ := workerRecoveryFixture(t, store, &fakeVMM{}, 0)
	if err := e.ReconcileWorkerPools(t.Context(), app.ID, TriggerWorkerPool); err != nil {
		t.Fatal(err)
	}
	before, err := store.WorkerRecoveryByApp(t.Context(), app.ID)
	if err != nil || len(before.Pools) != 1 {
		t.Fatalf("missing zero-floor observation: %+v %v", before, err)
	}
	manifest := app.Manifest
	manifest.ExecutionMode = api.ExecutionModeRequest
	if _, err := store.UpdateApp(t.Context(), app.ID, state.UpdateAppParams{Manifest: &manifest}); err != nil {
		t.Fatal(err)
	}
	if err := e.ReconcileWorkerPools(t.Context(), app.ID, TriggerWorkerPool); err != nil {
		t.Fatal(err)
	}
	after, err := store.WorkerRecoveryByApp(t.Context(), app.ID)
	if err != nil || len(after.Pools) != 0 {
		t.Fatalf("retired worker pool still visible: %+v %v", after, err)
	}
}

// adr: 20261017223000 — old draining workers never fill a new generation's target.
func TestWorkerRecoveryObservationsDistinguishRetiredGeneration(t *testing.T) {
	store := state.NewMemStore()
	e, app, old := workerRecoveryFixture(t, store, &fakeVMM{}, 2)
	dep, err := store.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Scope: "default",
		Kind: state.DeploymentKindImage, ImageDigest: "sha256:next-generation", Status: state.DeployLive})
	if err != nil {
		t.Fatal(err)
	}
	seedScopedWorker(t, store, app, dep)
	retired := seedScopedWorker(t, store, app, old)
	attempt := &workerRecoveryAttempt{lease: state.WorkerRecovery{Revision: "r"}, status: "ready"}
	plans := map[string]*workerScopePlan{"default": {scope: "default", target: dep.ID, desired: 2, workers: []state.Instance{retired}}}
	if err := e.observeWorkerRecoveryFleet(t.Context(), app.ID, plans, attempt); err != nil {
		t.Fatal(err)
	}
	if attempt.status != "waiting_capacity" || len(attempt.lease.Pools) != 1 {
		t.Fatalf("retired resident masked the missing target worker: %+v", attempt)
	}
	pool := attempt.lease.Pools[0]
	if pool.Desired != 2 || pool.Running != 1 || pool.Draining != 1 || pool.Starting != 0 {
		t.Fatalf("generation observations were conflated: %+v", pool)
	}
}

// adr: 20261017223000 — N-1 may update intent without knowing the observation column.
func TestWorkerRecoveryLegacyObservationsDoNotBlockCompletion(t *testing.T) {
	store := state.NewMemStore()
	e, app, dep := workerRecoveryFixture(t, store, &fakeVMM{}, 0)
	now := time.Now().UTC()
	e.now = func() time.Time { return now }
	lease, claimed, err := store.ClaimWorkerRecovery(t.Context(), app.ID, "", "current", uuid.NewString(), now, now.Add(time.Minute))
	if err != nil || !claimed {
		t.Fatalf("claim: %v %v", claimed, err)
	}
	// Model the JSON left by an older binary's explicit-column revision update.
	lease.Pools = []state.WorkerRecoveryPool{{Scope: "staging", DeploymentID: dep.ID,
		Revision: "previous", ObservedAt: now, Desired: 1}}
	attempt := &workerRecoveryAttempt{lease: lease, status: "ready", scoped: true, scope: "default"}
	e.completeWorkerRecovery(t.Context(), attempt, errors.New("demand unavailable"))
	row, err := store.WorkerRecoveryByApp(t.Context(), app.ID)
	if err != nil || row.ClaimToken != "" || row.Status != "waiting_dependency" || row.Failures != 1 || len(row.Pools) != 0 {
		t.Fatalf("legacy observations blocked completion: %+v %v", row, err)
	}
}

// adr: 20261017223000 — hot scoped events cannot starve a quiet pool's observation.
func TestWorkerRecoveryScopedSuccessDoesNotPostponeFullSweep(t *testing.T) {
	store := state.NewMemStore()
	account, app, _ := seedWorkerScopes(t, store, api.PlanPro)
	enqueueScopedDemand(t, store, account, app, "staging", "", 1)
	e := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0")
	e.workerReconcileSubmit = func(context.Context, string) {}
	now := time.Now().UTC()
	e.now = func() time.Time { return now }
	for range 4 {
		if err := e.ReconcileWorkerPoolForScope(t.Context(), app.ID, "default", 0, TriggerWorkerPool); err != nil {
			t.Fatal(err)
		}
		row, err := store.WorkerRecoveryByApp(t.Context(), app.ID)
		if err != nil || row.NextAttemptAt.After(now) {
			t.Fatalf("scoped success postponed the full sweep: %+v %v", row, err)
		}
		now = now.Add(time.Second)
	}
	loop := NewLoop(nil, e, testLog()).WithClock(e.now)
	loop.dispatchWorkerRecovery(t.Context())
	loop.workPool().drain()
	if got := scopedWorkerCounts(t, store, app.ID); got["default"] != 0 || got["staging"] != 1 {
		t.Fatalf("quiet environment never recovered: %+v", got)
	}
	row, err := store.WorkerRecoveryByApp(t.Context(), app.ID)
	if err != nil || len(row.Pools) != 2 || row.Status != "ready" {
		t.Fatalf("full sweep did not publish both pools: %+v %v", row, err)
	}
}

func TestWorkerRecoveryInactiveAccountsNeverBoot(t *testing.T) {
	ctx := context.Background()
	for _, status := range []state.AccountStatus{state.AccountSuspended, state.AccountDeletedPending} {
		t.Run(string(status), func(t *testing.T) {
			store := state.NewMemStore()
			vmm := &fakeVMM{}
			e, app, _ := workerRecoveryFixture(t, store, vmm, 1)
			if err := store.UpdateAccountStatus(ctx, app.AccountID, status); err != nil {
				t.Fatal(err)
			}
			loop := NewLoop(nil, e, testLog())
			loop.dispatchWorkerRecovery(ctx)
			loop.workPool().drain()
			e.ReconcileWorkerApp(ctx, app.ID)
			assertRecoveryWorkers(t, store, app.ID, 0)
			if vmm.coldBoots != 0 {
				t.Fatalf("inactive account booted %d workers", vmm.coldBoots)
			}
		})
	}
}
