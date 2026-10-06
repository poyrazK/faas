// adr: 420
package sched

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func recoveryFixture(t *testing.T, store state.Store, vmm *fakeVMM, desired int) (*Engine, state.App, state.Deployment) {
	t.Helper()
	_, app, dep := seedApp(t, store, api.PlanPro, 128, 5)
	// PgStore always inserts a pending deployment; make the fixture live
	// through the public promotion method rather than MemStore's shortcut.
	if err := store.MarkDeploymentLive(context.Background(), dep.ID); err != nil {
		t.Fatal(err)
	}
	dep, err := store.DeploymentByID(context.Background(), dep.ID)
	if err != nil {
		t.Fatal(err)
	}
	manifest := app.Manifest
	manifest.ExecutionMode = api.ExecutionModeService
	manifest.ServiceReplicas = &state.ServiceReplicas{Min: desired, Max: desired, Desired: desired}
	app, err = store.UpdateApp(context.Background(), app.ID, state.UpdateAppParams{Manifest: &manifest})
	if err != nil {
		t.Fatal(err)
	}
	e := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")
	// Tests explicitly run the loop; transition callbacks should never escape
	// into an untracked goroutine before a loop has been attached.
	e.serviceReconcileSubmit = func(context.Context, string) {}
	return e, app, dep
}

func assertRecoveryReplicas(t *testing.T, store state.Store, appID, depID string, want int) {
	t.Helper()
	rows, err := listServiceReplicas(context.Background(), store, appID, depID)
	if err != nil {
		t.Fatal(err)
	}
	actual := classifyServiceReplicas(rows)
	if actual.ready != want || actual.managed() != want {
		t.Fatalf("service replicas: %+v, want exactly %d ready/managed", actual, want)
	}
}

func TestServiceRecoveryRestartWithoutTraffic(t *testing.T) {
	store := state.NewMemStore()
	exerciseServiceRecoveryRestart(t, store, func() state.Store { return store })
}

// ADR-420 acceptance: one of two replicas disappears, replacement fails,
// schedd restarts during cooldown, then capacity returns. No Wake/request or
// notification is needed to restore exactly two replicas.
func exerciseServiceRecoveryRestart(t *testing.T, store state.Store, reopen func() state.Store) {
	t.Helper()
	ctx := context.Background()
	vmm := &fakeVMM{}
	e, app, dep := recoveryFixture(t, store, vmm, 2)
	var clock atomic.Int64
	clock.Store(time.Now().UTC().Truncate(time.Microsecond).UnixNano())
	now := func() time.Time { return time.Unix(0, clock.Load()).UTC() }
	e.now = now
	if err := e.convergeServiceReplicasToTarget(ctx, dep.ID, 2, true); err != nil {
		t.Fatalf("seed service replicas: %v", err)
	}
	assertRecoveryReplicas(t, store, app.ID, dep.ID, 2)
	rows, err := listServiceReplicas(ctx, store, app.ID, dep.ID)
	if err != nil {
		t.Fatal(err)
	}
	e.transition(ctx, rows[0].ID, app.ID, state.StateStopped)
	e.ledger.Release(rows[0].ID)
	vmm.mu.Lock()
	vmm.wakeErr = errors.New("vmmd temporarily unavailable")
	vmm.mu.Unlock()
	loop := NewLoop(nil, e, testLog()).WithClock(now)
	loop.dispatchServiceRecovery(ctx)
	loop.workPool().drain()
	assertRecoveryReplicas(t, store, app.ID, dep.ID, 1)
	retry, err := store.ServiceRecoveryByApp(ctx, app.ID)
	if err != nil || retry.Status != "retrying_startup" || retry.Failures != 1 || !retry.NextAttemptAt.Equal(now().Add(5*time.Second)) {
		t.Fatalf("startup retry not durable: %+v err=%v", retry, err)
	}
	// Both the engine and store client are replaced. Their process-local
	// counters/queues cannot be responsible for retaining this deadline.
	store = reopen()
	e = newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")
	e.now = now
	loop = NewLoop(nil, e, testLog()).WithClock(now)
	vmm.mu.Lock()
	vmm.wakeErr = nil
	vmm.mu.Unlock()
	loop.dispatchServiceRecovery(ctx)
	loop.workPool().drain()
	e.ReconcileServiceApp(ctx, app.ID) // an event must also respect cooldown
	assertRecoveryReplicas(t, store, app.ID, dep.ID, 1)
	clock.Add(int64(5 * time.Second))
	loop.dispatchServiceRecovery(ctx)
	loop.workPool().drain()
	assertRecoveryReplicas(t, store, app.ID, dep.ID, 2)
	retry, err = store.ServiceRecoveryByApp(ctx, app.ID)
	if err != nil || retry.Status != "ready" || retry.Failures != 0 || !retry.NextAttemptAt.Equal(now().Add(30*time.Second)) {
		t.Fatalf("recovery not completed: %+v err=%v", retry, err)
	}
	// Repeated sweeps after the healthy deadline must never admit a third VM.
	for range 3 {
		clock.Add(int64(30 * time.Second))
		loop.dispatchServiceRecovery(ctx)
		loop.workPool().drain()
		assertRecoveryReplicas(t, store, app.ID, dep.ID, 2)
	}
	vmm.mu.Lock()
	defer vmm.mu.Unlock()
	if vmm.coldBoots != 3 {
		t.Fatalf("successful boots=%d, want two initial and one replacement", vmm.coldBoots)
	}
}

func TestServiceRecoveryBackoffAndChangedIntent(t *testing.T) {
	store := state.NewMemStore()
	vmm := &fakeVMM{wakeErr: errors.New("boot failed")}
	e, app, _ := recoveryFixture(t, store, vmm, 1)
	var clock atomic.Int64
	clock.Store(time.Now().UnixNano())
	e.now = func() time.Time { return time.Unix(0, clock.Load()).UTC() }
	for _, delay := range []time.Duration{5 * time.Second, 10 * time.Second, 20 * time.Second} {
		e.ReconcileServiceApp(context.Background(), app.ID)
		r, err := store.ServiceRecoveryByApp(context.Background(), app.ID)
		if err != nil || r.Status != "retrying_startup" || !r.NextAttemptAt.Equal(e.now().Add(delay)) {
			t.Fatalf("retry delay %v: %+v %v", delay, r, err)
		}
		clock.Store(r.NextAttemptAt.UnixNano())
	}
	// Editing desired configuration is actionable even before an old deadline.
	clock.Add(-int64(time.Second))
	manifest := app.Manifest
	manifest.Env = map[string]string{"FIXED_CONFIG": "1"}
	if _, err := store.UpdateApp(context.Background(), app.ID, state.UpdateAppParams{Manifest: &manifest}); err != nil {
		t.Fatal(err)
	}
	vmm.mu.Lock()
	vmm.wakeErr = nil
	vmm.mu.Unlock()
	e.ReconcileServiceApp(context.Background(), app.ID)
	r, err := store.ServiceRecoveryByApp(context.Background(), app.ID)
	if err != nil || r.Status != "ready" || r.Failures != 0 {
		t.Fatalf("fixed intent remained in cooldown: %+v %v", r, err)
	}
	if got := serviceRecoveryBackoff(api.ServiceRecoveryFailureCountMax); got != 5*time.Minute {
		t.Fatalf("backoff cap=%v, want 5m", got)
	}
}

func TestServiceRecoveryConcurrentEnginesClaimOnce(t *testing.T) {
	store := state.NewMemStore()
	vmm := &fakeVMM{bootStarted: make(chan struct{}, 1), bootRelease: make(chan struct{})}
	e, app, dep := recoveryFixture(t, store, vmm, 1)
	peer := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")
	peer.serviceReconcileSubmit = func(context.Context, string) {}
	done := make(chan struct{})
	go func() {
		e.ReconcileServiceApp(context.Background(), app.ID)
		close(done)
	}()
	defer func() { close(vmm.bootRelease); <-done }()
	select {
	case <-vmm.bootStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("first scheduler did not enter boot")
	}
	peer.ReconcileServiceApp(context.Background(), app.ID)
	rows, err := listServiceReplicas(context.Background(), store, app.ID, dep.ID)
	if err != nil || len(rows) != 1 {
		t.Fatalf("second engine created extra in-flight VM: %d %v", len(rows), err)
	}
}

func TestServiceRecoverySaturatedPoolLeavesAppsDue(t *testing.T) {
	store := state.NewMemStore()
	e, app, dep := recoveryFixture(t, store, &fakeVMM{}, 1)
	loop := NewLoop(nil, e, testLog())
	release := fillSlots(t, loop.workPool(), workServiceRecovery)
	defer release()
	loop.runServiceRecovery(context.Background())
	if _, err := store.ServiceRecoveryByApp(context.Background(), app.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("dropped work should remain unclaimed: %v", err)
	}
	release()
	loop.workPool().drain()
	loop.dispatchServiceRecovery(context.Background())
	loop.workPool().drain()
	assertRecoveryReplicas(t, store, app.ID, dep.ID, 1)
}

func TestServiceRecoveryZeroDesiredStaysStopped(t *testing.T) {
	store := state.NewMemStore()
	e, app, dep := recoveryFixture(t, store, &fakeVMM{}, 0)
	loop := NewLoop(nil, e, testLog())
	loop.dispatchServiceRecovery(context.Background())
	loop.workPool().drain()
	assertRecoveryReplicas(t, store, app.ID, dep.ID, 0)
	r, err := store.ServiceRecoveryByApp(context.Background(), app.ID)
	if err != nil || r.Status != "ready" || r.Failures != 0 {
		t.Fatalf("zero desired recovery: %+v %v", r, err)
	}
}

func TestServiceRecoveryCapacityReturnsWithoutTraffic(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	e, app, dep := recoveryFixture(t, store, &fakeVMM{}, 2)
	node, err := store.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
	if err != nil {
		t.Fatal(err)
	}
	node.AdmissionCeilingMB = app.RAMMB + api.PerVMOverheadMB // room for only one
	if _, err := store.UpsertComputeNodeFromOperator(ctx, node); err != nil {
		t.Fatal(err)
	}
	var clock atomic.Int64
	clock.Store(time.Now().UnixNano())
	now := func() time.Time { return time.Unix(0, clock.Load()).UTC() }
	e.now = now
	loop := NewLoop(nil, e, testLog()).WithClock(now)
	loop.dispatchServiceRecovery(ctx)
	loop.workPool().drain()
	assertRecoveryReplicas(t, store, app.ID, dep.ID, 1)
	r, err := store.ServiceRecoveryByApp(ctx, app.ID)
	if err != nil || r.Status != "waiting_capacity" || r.Failures != 1 {
		t.Fatalf("capacity retry: %+v %v", r, err)
	}
	node.AdmissionCeilingMB *= 2
	if _, err := store.UpsertComputeNodeFromOperator(ctx, node); err != nil {
		t.Fatal(err)
	}
	clock.Store(r.NextAttemptAt.UnixNano())
	loop.dispatchServiceRecovery(ctx)
	loop.workPool().drain()
	assertRecoveryReplicas(t, store, app.ID, dep.ID, 2)
}

type recoveryDependencyStore struct {
	state.Store
	unavailable atomic.Bool
}

func (s *recoveryDependencyStore) ListMirrorRules(ctx context.Context, appID string) ([]state.MirrorRule, error) {
	if s.unavailable.Load() {
		return nil, errors.New("state dependency unavailable")
	}
	return s.Store.ListMirrorRules(ctx, appID)
}

func TestServiceRecoveryDependencyFailureRetries(t *testing.T) {
	store := &recoveryDependencyStore{Store: state.NewMemStore()}
	e, app, dep := recoveryFixture(t, store, &fakeVMM{}, 1)
	var clock atomic.Int64
	clock.Store(time.Now().UnixNano())
	e.now = func() time.Time { return time.Unix(0, clock.Load()).UTC() }
	store.unavailable.Store(true)
	e.ReconcileServiceApp(context.Background(), app.ID)
	r, err := store.ServiceRecoveryByApp(context.Background(), app.ID)
	if err != nil || r.Status != "waiting_dependency" || r.Failures != 1 || !r.NextAttemptAt.Equal(e.now().Add(5*time.Second)) {
		t.Fatalf("dependency retry: %+v %v", r, err)
	}
	store.unavailable.Store(false)
	clock.Store(r.NextAttemptAt.UnixNano())
	e.ReconcileServiceApp(context.Background(), app.ID)
	assertRecoveryReplicas(t, store, app.ID, dep.ID, 1)
}
