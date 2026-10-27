// adr: 062, 1151 — placement counts the guest vCPUs every owner booked on a
// node, not only this scheduler's own.
package sched_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// Another owner's instances fill the owner node's vCPU budget. This
// scheduler's ledger holds none of them, so placement used to see an empty
// budget there and land the wake on a full node. It now reads the store's
// fleet-wide count and goes to the peer.
func TestPlacementCountsAnotherOwnersVCPUs(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	existing, err := store.ListComputeNodes(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range existing {
		if err := store.MarkComputeNodeInactive(ctx, n.ID); err != nil {
			t.Fatal(err)
		}
	}
	vcpus := api.MustLimitsFor(api.PlanPro).VCPU
	f := &failoverFleet{store: store, ctx: ctx, vmm: newRefusingVMM(nil), ops: wire.NewOpsMetrics("schedd")}
	// The owner has more physical CPU, so CPU-first ranking and the owner
	// preference both pick it unless its vCPU budget is seen as full.
	for i, cpus := range []int{80, 40} {
		node, err := store.CreateComputeNode(ctx, state.ComputeNode{
			Name: fmt.Sprintf("vcpu-%d", i+1), TargetURL: fmt.Sprintf("tcp://10.0.3.%d:7000", i+2),
			VPCPUs: cpus, MemMB: 16384, MaxConcurrency: 50, AdmissionCeilingMB: 8192, VCPUBudget: 2 * vcpus, Active: true,
		})
		if err != nil {
			t.Fatal(err)
		}
		f.nodes = append(f.nodes, node)
	}
	f.e = sched.XNewEngine(t, store, f.vmm, &sched.FakeNotifier{}, "1.10.0").
		WithOwnerNodeID(f.nodes[0].ID).WithOpsMetrics(f.ops)
	owner, peer := f.nodes[0], f.nodes[1]

	other := f.app(t, api.PlanPro, 256, 5)
	dep, err := store.LiveDeployment(ctx, other.ID)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := store.CreateInstanceWithMode(ctx, other.ID, dep.ID, string(state.StateRunning), 256, owner.ID, uuid.NewString(), string(state.InstanceModeNormal)); err != nil {
			t.Fatal(err)
		}
	}

	res, err := f.e.Wake(ctx, f.app(t, api.PlanPro, 256, 1).ID, "", "", sched.TriggerCronSched)
	if err != nil {
		t.Fatalf("wake with the peer free: %v", err)
	}
	if res.NodeID != peer.ID {
		t.Fatalf("wake placed on %s, want the peer %s: the owner's vCPU budget is full", res.NodeID, peer.ID)
	}
}
