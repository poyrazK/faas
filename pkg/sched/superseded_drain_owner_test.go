package sched_test

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// Prod test 2026-11-15: every schedd receives deployment_changed, and the
// superseded drain's idle-park step (#7246) did not check ownership. A
// non-owner parked the owner's instance first, the owner's own park found it
// already parked and returned without releasing its reservation, and the
// owner then refused explicit admissions (the deployment smoke) at the cap.
func TestSupersededDrainOnlyRunsOnTheOwner(t *testing.T) {
	defer sched.SetSupersededDrainPoll(time.Hour)()
	ctx := context.Background()
	mem, owner, app, dep, ids := seedSupersededDrain(ctx, t, api.PlanPro, 5, 1)
	if err := mem.SetAppNodeID(ctx, app.ID, *owner.DefaultLocalNodeID()); err != nil {
		t.Fatal(err)
	}
	peer := sched.XNewEngine(t, mem, &sched.FakeVMM{}, &sched.FakeNotifier{}, "1.10.0").WithOwnerNodeID("peer-node")

	sched.NewLoop(nil, peer, nil).WithInstanceActivity(supersededActivity{}).DrainSupersededDeployment(ctx, dep.ID, time.Now().Add(time.Minute))
	if got := supersededInstanceState(ctx, t, mem, ids[0]); got != string(state.StateRunning) {
		t.Fatalf("a peer scheduler's drain moved the owner's instance to %s; only the owner may park it", got)
	}

	sched.NewLoop(nil, owner, nil).WithInstanceActivity(supersededActivity{}).DrainSupersededDeployment(ctx, dep.ID, time.Now().Add(time.Minute))
	if got := supersededInstanceState(ctx, t, mem, ids[0]); got != string(state.StateParked) {
		t.Fatalf("owner drain left the instance %s, want parked", got)
	}
	if owner.Ledger().ResidentFor(ids[0]) {
		t.Fatal("owner still holds the parked instance's reservation")
	}
}

// An operator intent claimed by a peer scheduler stops the instance and
// releases the peer's ledger, not the owner's; the owner never parks that
// instance again. The periodic audit releases it without waiting for an
// admission to hit the cap.
func TestReservationAuditReleasesOnlyInstancesNoLongerResident(t *testing.T) {
	ctx := context.Background()
	mem, e, app, _, ids := seedSupersededDrain(ctx, t, api.PlanPro, 5, 2)
	stopped, running := ids[0], ids[1]
	if err := mem.UpdateInstanceStateToTerminal(ctx, stopped, string(state.StateStopped), time.Now()); err != nil {
		t.Fatal(err)
	}
	if got := e.ReleaseStaleReservations(ctx); got != 1 {
		t.Fatalf("audit released %d reservations, want 1", got)
	}
	if e.Ledger().ResidentFor(stopped) {
		t.Fatal("stopped instance's reservation still held")
	}
	if !e.Ledger().ResidentFor(running) || e.Ledger().Concurrency(app.ID) != 1 {
		t.Fatal("audit released the running instance's reservation")
	}
}

// A park that finds the row already moved by another scheduler (an operator
// intent any schedd may claim, a drain on a peer) must release this ledger's
// reservation instead of returning silently.
func TestParkReleasesTheReservationOfAnInstanceAPeerMoved(t *testing.T) {
	ctx := context.Background()
	for _, moved := range []state.State{state.StateParked, state.StateStopped, state.StateFailed} {
		t.Run(string(moved), func(t *testing.T) {
			mem, e, app, _, ids := seedSupersededDrain(ctx, t, api.PlanFree, 1, 1)
			if err := mem.UpdateInstanceStateToTerminal(ctx, ids[0], string(moved), time.Now()); err != nil {
				if err := mem.UpdateInstanceState(ctx, ids[0], string(moved)); err != nil {
					t.Fatal(err)
				}
			}
			if !e.Ledger().ResidentFor(ids[0]) {
				t.Fatal("fixture: the reservation should still be held")
			}
			if err := e.Park(ctx, ids[0]); err != nil {
				t.Fatalf("Park: %v", err)
			}
			if e.Ledger().ResidentFor(ids[0]) || e.Ledger().Concurrency(app.ID) != 0 {
				t.Fatalf("reservation of a %s instance still held after Park", moved)
			}
		})
	}
}
