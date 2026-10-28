// adr: 20261028200212 — the stuck-row watchdog acts only on its owner's apps,
// and the pressure rebalancer moves an app only while it has no resident
// instance, checked atomically with the admissions it races.
package sched_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

// Every scheduler runs the watchdog over the whole fleet. A peer used to
// tear down the owner's stuck cold boot too: N schedulers each destroyed the
// VM, emitted the stall and wrote the transition, and appMu, which keeps a
// kill from racing the owner's own publication, is process-local.
func TestWatchdogSweepsOnlyOwnedApps(t *testing.T) {
	ctx := context.Background()
	f := newFailoverFleet(t, 2, 8192, func(string) error { return nil })
	owner, peer := f.nodes[0], f.nodes[1]
	app := f.app(t, api.PlanPro, 256, 1)
	dep, err := f.store.LiveDeployment(ctx, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	stuck, err := f.store.CreateInstanceWithMode(ctx, app.ID, dep.ID, string(state.StateColdBooting), 256, owner.ID, uuid.NewString(), string(state.InstanceModeNormal))
	if err != nil {
		t.Fatal(err)
	}
	late := func() time.Time { return time.Now().Add(time.Hour) }
	rowState := func() string {
		t.Helper()
		ins, err := f.store.InstanceByID(ctx, stuck.ID)
		if err != nil {
			t.Fatal(err)
		}
		return ins.State
	}

	peerVMM := &sched.FakeVMM{}
	peerEngine := sched.XNewEngine(t, f.store, peerVMM, &sched.FakeNotifier{}, "1.10.0").WithOwnerNodeID(peer.ID)
	sched.NewWatchdog(f.store, peerEngine, nil).WithClock(late).SweepRuns(ctx)
	if got := rowState(); got != string(state.StateColdBooting) {
		t.Fatalf("a peer's watchdog moved the owner's stuck row to %s", got)
	}
	if n := *peerVMM.Destroys(); n != 0 {
		t.Fatalf("a peer's watchdog destroyed the owner's VM %d times", n)
	}

	sched.NewWatchdog(f.store, f.e, nil).WithClock(late).SweepRuns(ctx)
	if got := rowState(); got != string(state.StateFailed) {
		t.Fatalf("the owner's watchdog left its stuck cold boot %s, want FAILED", got)
	}
}

// staleResidentStore answers the rebalancer's resident-instance pre-check
// from before a wake's instance landed.
type staleResidentStore struct {
	MemStore *state.MemStore
}

func (s *staleResidentStore) ListInstancesForApp(context.Context, string) ([]state.Instance, error) {
	return nil, nil
}

// A wake that passed its ownership check lands its instance between the
// rebalancer's pre-check and the move. The move used to go through and leave
// the instance outside the new owner's ledger (H9-101); it now re-checks
// under the app row lock and keeps the app where it is.
func TestPressureRebalanceKeepsAnAppWithResidentInstances(t *testing.T) {
	ctx := context.Background()
	f := newFailoverFleet(t, 2, 8192, func(string) error { return nil })
	owner := f.nodes[0]
	app := f.app(t, api.PlanPro, 256, 1)
	dep, err := f.store.LiveDeployment(ctx, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.CreateInstanceWithMode(ctx, app.ID, dep.ID, string(state.StateRunning), 256, owner.ID, uuid.NewString(), string(state.InstanceModeNormal)); err != nil {
		t.Fatal(err)
	}
	e := sched.XNewEngine(t, statetest.NewMem(&staleResidentStore{MemStore: f.store}), &sched.FakeVMM{}, &sched.FakeNotifier{}, "1.10.0").
		WithOwnerNodeID(owner.ID)
	if err := e.RebalancePressuredApps(ctx, app.ID); err != nil {
		t.Fatalf("RebalancePressuredApps: %v", err)
	}
	got, err := f.store.AppByID(ctx, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.NodeID != owner.ID {
		t.Fatalf("app with a running instance moved to %s, want it kept on %s", got.NodeID, owner.ID)
	}
}
