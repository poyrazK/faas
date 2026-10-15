// adr: 732
package sched

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

type fakeForkRuntime struct {
	mu         sync.Mutex
	restoreErr error
	restored   []string
	destroyed  []string
}

func (f *fakeForkRuntime) RestoreFork(_ context.Context, fork state.AppFork) (ForkRestore, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.restoreErr != nil {
		return ForkRestore{}, f.restoreErr
	}
	f.restored = append(f.restored, fork.ID)
	return ForkRestore{InstanceID: uuid.NewString(), SnapshotID: uuid.NewString(), NodeID: "node-1"}, nil
}

func (f *fakeForkRuntime) DestroyForkInstance(_ context.Context, _, instanceID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.destroyed = append(f.destroyed, instanceID)
	return nil
}

type forkCoordinatorFixture struct {
	store   *state.MemStore
	runtime *fakeForkRuntime
	clock   time.Time
	acct    state.Account
	app     state.App
	dep     state.Deployment
}

func newForkCoordinatorFixture(t *testing.T) *forkCoordinatorFixture {
	t.Helper()
	store := state.NewMemStore()
	acct, app, dep := seedApp(t, store, api.PlanPro, 256, 5)
	return &forkCoordinatorFixture{store: store, runtime: &fakeForkRuntime{},
		clock: time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC), acct: acct, app: app, dep: dep}
}

func (f *forkCoordinatorFixture) coordinator(owner string, maxConcurrent int) *ForkCoordinator {
	c := NewForkCoordinator(f.store, f.runtime, ForkCoordinatorConfig{Owner: owner, Lease: time.Minute, MaxConcurrent: maxConcurrent}, nil)
	c.now = func() time.Time { return f.clock }
	return c
}

func (f *forkCoordinatorFixture) createFork(t *testing.T, ttl int) state.AppFork {
	t.Helper()
	fork, err := f.store.CreateAppFork(context.Background(), state.CreateAppForkParams{
		AccountID: f.acct.ID, AppID: f.app.ID, DeploymentID: f.dep.ID, RequestedBy: "user:test",
		TTLSeconds: ttl, MaxPerApp: 10, MaxPerAccount: 10, CreatedAt: f.clock,
	})
	if err != nil {
		t.Fatalf("CreateAppFork: %v", err)
	}
	return fork
}

func (f *forkCoordinatorFixture) fork(t *testing.T, id string) state.AppFork {
	t.Helper()
	fork, err := f.store.AppForkByID(context.Background(), f.acct.ID, f.app.ID, id)
	if err != nil {
		t.Fatal(err)
	}
	return fork
}

func TestForkCoordinator_RestoresThenDestroysAtTTL(t *testing.T) {
	f := newForkCoordinatorFixture(t)
	c := f.coordinator("schedd-a", 4)
	fork := f.createFork(t, 600)

	c.Tick(context.Background())
	running := f.fork(t, fork.ID)
	if running.Status != state.AppForkRunning || running.InstanceID == nil || c.Held() != 1 {
		t.Fatalf("after first tick: %+v held=%d, want running and held", running, c.Held())
	}

	f.clock = f.clock.Add(5 * time.Minute)
	c.Tick(context.Background())
	if got := f.fork(t, fork.ID); got.Status != state.AppForkRunning || len(f.runtime.destroyed) != 0 {
		t.Fatalf("before TTL: status %s, destroyed %v", got.Status, f.runtime.destroyed)
	}

	f.clock = f.clock.Add(6 * time.Minute)
	c.Tick(context.Background())
	done := f.fork(t, fork.ID)
	if done.Status != state.AppForkExpired || len(f.runtime.destroyed) != 1 || f.runtime.destroyed[0] != *running.InstanceID || c.Held() != 0 {
		t.Fatalf("after TTL: %+v destroyed=%v held=%d, want expired with its instance destroyed", done, f.runtime.destroyed, c.Held())
	}
}

func TestForkCoordinator_RestoreFailureFailsTheForkWithACode(t *testing.T) {
	f := newForkCoordinatorFixture(t)
	f.runtime.restoreErr = ErrForkNoCapture
	c := f.coordinator("schedd-a", 4)
	fork := f.createFork(t, 600)

	c.Tick(context.Background())
	got := f.fork(t, fork.ID)
	if got.Status != state.AppForkFailed || got.FailureCode == nil || *got.FailureCode != "no_capture" || c.Held() != 0 {
		t.Fatalf("fork = %+v held=%d, want failed no_capture", got, c.Held())
	}
}

func TestForkCoordinator_RecordsRestoreMetrics(t *testing.T) {
	f := newForkCoordinatorFixture(t)
	metrics := wire.NewCrashForkMetrics(prometheus.NewRegistry())
	c := NewForkCoordinator(f.store, f.runtime, ForkCoordinatorConfig{Owner: "schedd-a", Lease: time.Minute, MaxConcurrent: 4, Metrics: metrics}, nil)
	c.now = func() time.Time { return f.clock }
	f.createFork(t, 600)
	f.clock = f.clock.Add(3 * time.Second)
	c.Tick(context.Background())
	if n := testutil.ToFloat64(metrics.ForkRestoresTotal.WithLabelValues("deployment", "running")); n != 1 {
		t.Fatalf("schedd_fork_restores_total{deployment,running} = %v, want 1", n)
	}
	if n := testutil.CollectAndCount(metrics.ForkClaimWait); n != 1 {
		t.Fatalf("claim wait series = %d, want 1", n)
	}

	f.runtime.restoreErr = ErrForkNoCapture
	f.createFork(t, 600)
	c.Tick(context.Background())
	if n := testutil.ToFloat64(metrics.ForkRestoresTotal.WithLabelValues("deployment", "no_capture")); n != 1 {
		t.Fatalf("schedd_fork_restores_total{deployment,no_capture} = %v, want 1", n)
	}
}

func TestForkCoordinator_CancelDestroysARunningFork(t *testing.T) {
	f := newForkCoordinatorFixture(t)
	c := f.coordinator("schedd-a", 4)
	fork := f.createFork(t, 3600)
	c.Tick(context.Background())

	if _, err := f.store.RequestAppForkCancellation(context.Background(), f.acct.ID, f.app.ID, fork.ID, f.clock.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	f.clock = f.clock.Add(2 * time.Second)
	c.Tick(context.Background())
	if got := f.fork(t, fork.ID); got.Status != state.AppForkCancelled || len(f.runtime.destroyed) != 1 {
		t.Fatalf("fork = %+v destroyed=%v, want cancelled and destroyed", got, f.runtime.destroyed)
	}
}

// A scheduler that stops renewing loses its forks: another coordinator
// adopts a running fork (it lives out its TTL) and stays the only owner.
func TestForkCoordinator_TakesOverAnAbandonedRunningFork(t *testing.T) {
	f := newForkCoordinatorFixture(t)
	a, b := f.coordinator("schedd-a", 4), f.coordinator("schedd-b", 4)
	fork := f.createFork(t, 3600)
	a.Tick(context.Background())

	f.clock = f.clock.Add(2 * time.Minute) // a never renews: its lease lapsed
	b.Tick(context.Background())
	adopted := f.fork(t, fork.ID)
	if adopted.Status != state.AppForkRunning || adopted.LeaseOwner == nil || *adopted.LeaseOwner != "schedd-b" || b.Held() != 1 {
		t.Fatalf("fork = %+v b.held=%d, want running and adopted by schedd-b", adopted, b.Held())
	}
	if len(f.runtime.destroyed) != 0 {
		t.Fatalf("adoption destroyed the VM: %v", f.runtime.destroyed)
	}

	a.Tick(context.Background()) // the old owner finds its lease gone
	if a.Held() != 0 {
		t.Fatalf("schedd-a still holds %d forks after losing the lease", a.Held())
	}
}

func TestForkCoordinator_RespectsMaxConcurrent(t *testing.T) {
	f := newForkCoordinatorFixture(t)
	c := f.coordinator("schedd-a", 2)
	for range 3 {
		f.createFork(t, 600)
	}
	c.Tick(context.Background())
	if len(f.runtime.restored) != 2 || c.Held() != 2 {
		t.Fatalf("restored %d held %d, want 2 (MaxConcurrent)", len(f.runtime.restored), c.Held())
	}
}

func TestForkFailureCodeMapsCapacity(t *testing.T) {
	if code, _ := ForkFailureCode(api.ErrCapacity("full")); code != "no_capacity" {
		t.Errorf("capacity problem code = %q, want no_capacity", code)
	}
	if code, _ := ForkFailureCode(errors.New("boom")); code != "restore_failed" {
		t.Errorf("unknown error code = %q, want restore_failed", code)
	}
}
