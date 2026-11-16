// adr: 20261110210000
package sched_test

import (
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// The control plane's recovery spreads a dead node's apps over the other
// admitting nodes by owned load, never back onto the dead node, and the
// scheduler running it needs no node of its own.
func TestCentralOwnershipRecoverySpreadsOrphansByLoad(t *testing.T) {
	store, ctx, first, source := rebalanceTestOwners(t)
	second, err := store.CreateComputeNode(ctx, state.ComputeNode{
		Name: "fsn-second-" + uuid.NewString()[:8], TargetURL: "tcp://10.0.0.3:7000",
		ScheddTargetURL: ptrStringRebalance("tcp://10.0.0.3:7100"),
		VPCPUs:          80, MemMB: 28000, MaxConcurrency: 20,
		AdmissionCeilingMB: first.AdmissionCeilingMB, VCPUBudget: 80, Active: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	var apps []state.App
	for range 4 {
		apps = append(apps, seedAppOnNode(t, store, ctx, api.PlanHobby, 128, source.ID))
	}
	if err := store.SetComputeNodeActive(ctx, source.ID, false); err != nil {
		t.Fatal(err)
	}
	notif := &sched.FakeNotifier{}
	e := newRebalanceEngine(t, store, "", notif)
	if err := e.RebalanceOrphanedAppsCentral(ctx, source.ID); err != nil {
		t.Fatal(err)
	}
	perNode := map[string]int{}
	for _, a := range apps {
		got, err := store.AppByID(ctx, a.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.NodeID == source.ID {
			t.Fatalf("app %s left on the dead node", a.ID)
		}
		perNode[got.NodeID]++
	}
	if perNode[first.ID] != 2 || perNode[second.ID] != 2 {
		t.Fatalf("orphans not balanced over the admitting nodes: %v (first=%s second=%s)", perNode, first.ID, second.ID)
	}
}

// A node without headroom for one more instance is skipped; with no
// candidate left the app stays orphaned for the next sweep.
func TestCentralOwnershipRecoveryRespectsHeadroom(t *testing.T) {
	store, ctx, only, source := rebalanceTestOwners(t)
	app := seedAppOnNode(t, store, ctx, api.PlanHobby, 512, source.ID)
	only.AdmissionCeilingMB = 128 + api.PerVMOverheadMB
	if _, err := store.UpsertComputeNodeFromOperator(ctx, only); err != nil {
		t.Fatal(err)
	}
	if err := store.SetComputeNodeActive(ctx, source.ID, false); err != nil {
		t.Fatal(err)
	}
	e := newRebalanceEngine(t, store, "", &sched.FakeNotifier{})
	if err := e.RebalanceOrphanedAppsCentral(ctx, source.ID); err != nil {
		t.Fatal(err)
	}
	got, err := store.AppByID(ctx, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.NodeID != source.ID {
		t.Fatalf("app moved to %s without headroom", got.NodeID)
	}
}
