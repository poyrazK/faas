// adr: 909 amendment 1, 1528, 640 — measured admission books past the bare
// ceiling through the durable reservation, a durable capacity refusal is
// re-placed, and a coordinated wake's leader outlives the boot it starts.
package sched_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// wakeOne wakes one app outside the gateway path, so a capacity refusal is
// reported rather than answered by parking another app (ADR-643).
func wakeOne(t *testing.T, f *failoverFleet, app state.App) (sched.WakeResult, bool) {
	t.Helper()
	res, err := f.e.Wake(f.ctx, app.ID, "", "", sched.TriggerCronSched)
	var problem *api.Problem
	if errors.As(err, &problem) && problem.Code == api.CodeCapacity {
		return sched.WakeResult{}, false
	}
	if err != nil {
		t.Fatalf("wake %s: %v", app.Slug, err)
	}
	return res, true
}

// A scheduler admitting by a fresh measurement of its node books
// reservations up to ADR-909's overcommit bound. The durable per-node guard
// used to refuse every admission past the bare ceiling, so the node stopped
// at half the density measured admission promises.
func TestMeasuredAdmissionBooksPastTheCeiling(t *testing.T) {
	const ceilingMB, ramMB = 1024, 248
	f := newFailoverFleet(t, 1, ceilingMB, func(string) error { return nil })
	node := f.nodes[0]
	f.e.Ledger().EnableMeasuredAdmission(func(string) (int, time.Time, bool) {
		return 64, time.Now(), true
	})
	// vmmd's live report for the scheduler's own node carries measured RSS.
	f.e.CapacityTable().Replace(sched.CapacityReport{NodeID: node.ID, SampledAt: time.Now(), UsedMB: 64, RAMHeadroomMB: int32(node.MemMB - 64)})

	perInstance := ramMB + api.PerVMOverheadMB
	want := ceilingMB * api.MeasuredAdmissionMaxOvercommitPercent / 100 / perInstance
	admitted := 0
	for range want + 2 {
		if _, ok := wakeOne(t, f, f.app(t, api.PlanPro, ramMB, 1)); ok {
			admitted++
		}
	}
	if admitted != want {
		t.Fatalf("measured admission admitted %d instances of %d MB on a %d MB node, want %d (the %d%% reservation bound)",
			admitted, perInstance, ceilingMB, want, api.MeasuredAdmissionMaxOvercommitPercent)
	}
	used, err := f.store.ComputeNodeUsedMB(f.ctx, node.ID)
	if err != nil {
		t.Fatal(err)
	}
	if used <= ceilingMB || used > int64(ceilingMB*api.MeasuredAdmissionMaxOvercommitPercent/100) {
		t.Fatalf("node books %d MB, want past the %d MB ceiling and within the overcommit bound", used, ceilingMB)
	}
}

// Without a measurement the bare ceiling still binds.
func TestReservationAdmissionStopsAtTheCeiling(t *testing.T) {
	const ceilingMB, ramMB = 1024, 248
	f := newFailoverFleet(t, 1, ceilingMB, func(string) error { return nil })
	perInstance := ramMB + api.PerVMOverheadMB
	want := ceilingMB / perInstance
	admitted := 0
	for range want + 2 {
		if _, ok := wakeOne(t, f, f.app(t, api.PlanPro, ramMB, 1)); ok {
			admitted++
		}
	}
	if admitted != want {
		t.Fatalf("reservation admission admitted %d, want %d", admitted, want)
	}
}

// Another scheduler's instances fill the owner node, which this scheduler's
// ledger and vmmd's last report have not seen. Placement still picks the
// owner node; the durable reservation refuses it, and the wake lands on the
// peer instead of failing with a capacity error.
func TestWakeReplacesANodeTheDurableReservationFindsFull(t *testing.T) {
	const ceilingMB, ramMB = 1024, 248
	f := newFailoverFleet(t, 2, ceilingMB, func(string) error { return nil })
	owner, peer := f.nodes[0], f.nodes[1]
	fill := func(nodeID string, ramMB, count int) {
		t.Helper()
		other := f.app(t, api.PlanPro, ramMB, 5)
		dep, err := f.store.LiveDeployment(f.ctx, other.ID)
		if err != nil {
			t.Fatal(err)
		}
		for range count {
			if _, err := f.store.CreateInstanceWithMode(f.ctx, other.ID, dep.ID, string(state.StateRunning), ramMB, nodeID, uuid.NewString(), string(state.InstanceModeNormal)); err != nil {
				t.Fatal(err)
			}
		}
	}
	perInstance := ramMB + api.PerVMOverheadMB
	fill(owner.ID, ramMB, ceilingMB/perInstance)
	// The same CPU booked on the peer keeps the owner preferred on the
	// CPU-first ranking, with little RAM so the peer still has room.
	fill(peer.ID, 64, ceilingMB/perInstance)
	f.e.CapacityTable().Replace(sched.CapacityReport{NodeID: owner.ID, SampledAt: time.Now(), UsedMB: 64, RAMHeadroomMB: int32(owner.MemMB - 64)})

	res, ok := wakeOne(t, f, f.app(t, api.PlanPro, ramMB, 1))
	if !ok {
		t.Fatal("wake refused for capacity while the peer node has room")
	}
	if res.NodeID != peer.ID {
		t.Fatalf("wake placed on %s, want the peer %s", res.NodeID, peer.ID)
	}
}

// A coordinated wake's leader must outlive the cold-boot budget a snapshot
// wake may spend after a refused restore (ADR-640), and still return inside
// the main loop's stall budget, where explicit app wakes run.
func TestWakeLeaderBudgetCoversTheColdBoot(t *testing.T) {
	f := newFailoverFleet(t, 1, 1024, func(string) error { return nil })
	got := f.e.XWakeLeaderBudget()
	if got <= sched.ColdBootTimeout {
		t.Fatalf("leader budget %s does not cover the %s cold-boot budget", got, sched.ColdBootTimeout)
	}
	if hold := time.Duration(api.WakeQueueTTLSeconds) * time.Second; got < hold {
		t.Fatalf("leader budget %s is shorter than the %s wake queue hold", got, hold)
	}
	if got >= sched.MainLoopBudget {
		t.Fatalf("leader budget %s reaches the %s main loop stall budget", got, sched.MainLoopBudget)
	}
}
