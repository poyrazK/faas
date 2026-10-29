// adr: 20261027220445 — a wake placed on a stale view of a node's vCPU is
// refused by the durable reservation and re-placed.
package sched_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
	"github.com/onebox-faas/faas/pkg/wire"
)

// staleComputeViewStore hides the store's fleet-wide vCPU and CPU
// aggregates, so placement sees only this scheduler's ledger: the view two
// owner schedds both hold when they admit to one node at the same moment.
type staleComputeViewStore struct {
	Store state.Store
}

// Instances the ledger has not seen fill the owner node's vCPU budget.
// Placement chooses that node; the durable reservation, which sums every
// live instance under the node lock, refuses it, and the wake goes to the
// peer instead of overbooking the node.
func TestDurableReservationRefusesUnseenVCPU(t *testing.T) {
	ctx := context.Background()
	mem := state.NewMemStore()
	existing, err := mem.ListComputeNodes(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range existing {
		if err := mem.MarkComputeNodeInactive(ctx, n.ID); err != nil {
			t.Fatal(err)
		}
	}
	vcpus := api.MustLimitsFor(api.PlanPro).VCPU
	f := &failoverFleet{store: mem, ctx: ctx, vmm: newRefusingVMM(nil), ops: wire.NewOpsMetrics("schedd")}
	// The owner has more physical CPU, so CPU-first ranking and the owner
	// preference both pick it.
	for i, cpus := range []int{80, 40} {
		node, err := mem.CreateComputeNode(ctx, state.ComputeNode{
			Name: fmt.Sprintf("guard-%d", i+1), TargetURL: fmt.Sprintf("tcp://10.0.4.%d:7000", i+2),
			VPCPUs: cpus, MemMB: 16384, MaxConcurrency: 50, AdmissionCeilingMB: 8192, VCPUBudget: 2 * vcpus, Active: true,
		})
		if err != nil {
			t.Fatal(err)
		}
		f.nodes = append(f.nodes, node)
	}
	f.e = sched.XNewEngine(t, statetest.NewStore(&staleComputeViewStore{Store: mem}), f.vmm, &sched.FakeNotifier{}, "1.10.0").
		WithOwnerNodeID(f.nodes[0].ID).WithOpsMetrics(f.ops)
	owner, peer := f.nodes[0], f.nodes[1]

	other := f.app(t, api.PlanPro, 256, 5)
	dep, err := mem.LiveDeployment(ctx, other.ID)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := mem.CreateInstanceWithMode(ctx, other.ID, dep.ID, string(state.StateRunning), 256, owner.ID, uuid.NewString(), string(state.InstanceModeNormal)); err != nil {
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
