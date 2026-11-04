// adr: 20261026140000 — service replicas and minimum-instance floors spread
// across failure zones and nodes; spreading is a preference among fitting
// nodes, never a capacity gate.
package sched_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

func spreadNode(id, zone string, vpcpus int) state.ComputeNode {
	return state.ComputeNode{
		ID: id, Name: id, TargetURL: "unix:///run/faas/" + id + ".sock",
		VPCPUs: vpcpus, MemMB: 8192, MaxConcurrency: 200, AdmissionCeilingMB: 4096, VCPUBudget: 160,
		Region: strPointer("eu"), Zone: strPointer(zone),
		Lifecycle: state.NodeLifecycleActive, LifecycleGeneration: 1, Active: true,
	}
}

func strPointer(s string) *string { return &s }

func TestChoosePlacementSpreadsReplicas(t *testing.T) {
	base := sched.Request{AppID: "app", Plan: api.PlanPro, RAMMB: 256, VCPU: 1, CPUMillicores: 1000}
	for _, tc := range []struct {
		name      string
		nodes     []state.ComputeNode
		usedMB    map[string]int64
		instances map[string]int
		mutate    func(*sched.Request)
		want      string
	}{
		{
			name:      "the owner's preference yields to a node without a replica",
			nodes:     []state.ComputeNode{spreadNode("a", "z1", 16), spreadNode("b", "z1", 16), spreadNode("c", "z1", 16)},
			instances: map[string]int{"a": 1},
			mutate:    func(r *sched.Request) { r.PreferredNodeID = "a" },
			want:      "b",
		},
		{
			name:      "an empty zone before a roomier node in the occupied zone",
			nodes:     []state.ComputeNode{spreadNode("a", "z1", 16), spreadNode("b", "z1", 160), spreadNode("c", "z2", 16)},
			instances: map[string]int{"a": 1},
			want:      "c",
		},
		{
			name:      "then the empty node in a zone that holds one",
			nodes:     []state.ComputeNode{spreadNode("a", "z1", 16), spreadNode("b", "z1", 16), spreadNode("c", "z2", 160)},
			instances: map[string]int{"a": 1, "c": 1},
			want:      "b",
		},
		{
			name:      "a bigger node does not gather every replica",
			nodes:     []state.ComputeNode{spreadNode("big", "z1", 160), spreadNode("small", "z1", 4)},
			instances: map[string]int{"big": 1},
			want:      "small",
		},
		{
			name:      "a snapshot-holding node does not gather every replica",
			nodes:     []state.ComputeNode{spreadNode("a", "z1", 16), spreadNode("b", "z1", 16)},
			instances: map[string]int{"a": 1},
			mutate:    func(r *sched.Request) { r.PreferredNodeIDs = []string{"a"} },
			want:      "b",
		},
		{
			name:      "a node without room is passed over, not waited for",
			nodes:     []state.ComputeNode{spreadNode("a", "z1", 16), spreadNode("b", "z2", 16)},
			usedMB:    map[string]int64{"b": 4096},
			instances: map[string]int{"a": 2},
			want:      "a",
		},
		{
			name:      "the only fitting node still admits",
			nodes:     []state.ComputeNode{spreadNode("a", "z1", 16)},
			instances: map[string]int{"a": 3},
			want:      "a",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := base
			r.SpreadReplicas, r.AppInstancesByNode = true, tc.instances
			if tc.mutate != nil {
				tc.mutate(&r)
			}
			usedMB := tc.usedMB
			if usedMB == nil {
				usedMB = map[string]int64{}
			}
			got, err := sched.ChoosePlacement(tc.nodes, usedMB, nil, r)
			if err != nil {
				t.Fatal(err)
			}
			if got.NodeID != tc.want {
				t.Fatalf("placed on %s, want %s", got.NodeID, tc.want)
			}
		})
	}
}

// Without SpreadReplicas the ranking is unchanged: physical CPU headroom
// still sends an ordinary admission to the bigger node.
func TestChoosePlacementWithoutSpreadKeepsTheRanking(t *testing.T) {
	nodes := []state.ComputeNode{spreadNode("big", "z1", 160), spreadNode("small", "z1", 4)}
	r := sched.Request{AppID: "app", Plan: api.PlanPro, RAMMB: 256, VCPU: 1, CPUMillicores: 1000,
		AppInstancesByNode: map[string]int{"big": 1}}
	got, err := sched.ChoosePlacement(nodes, map[string]int64{}, nil, r)
	if err != nil {
		t.Fatal(err)
	}
	if got.NodeID != "big" {
		t.Fatalf("placed on %s, want big", got.NodeID)
	}
}

// newSpreadFleet is newFailoverFleet with one node per entry of vpcpus, so
// the nodes differ in physical CPU. The scheduler owns the first node.
func newSpreadFleet(t *testing.T, vpcpus ...int) *failoverFleet {
	t.Helper()
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
	f := &failoverFleet{store: store, ctx: ctx, vmm: newRefusingVMM(nil), ops: wire.NewOpsMetrics("schedd")}
	for i, cpus := range vpcpus {
		node, err := store.CreateComputeNode(ctx, state.ComputeNode{
			Name: fmt.Sprintf("spread-%d", i+1), TargetURL: fmt.Sprintf("tcp://10.0.2.%d:7000", i+2),
			VPCPUs: cpus, MemMB: 8192, MaxConcurrency: 50, AdmissionCeilingMB: 4096, VCPUBudget: 80, Active: true,
		})
		if err != nil {
			t.Fatal(err)
		}
		f.nodes = append(f.nodes, node)
	}
	f.e = sched.XNewEngine(t, store, f.vmm, &sched.FakeNotifier{}, "1.10.0").
		WithOwnerNodeID(f.nodes[0].ID).WithOpsMetrics(f.ops)
	return f
}

// On unequal nodes CPU-first placement put every service replica on the
// bigger node, which is also the owner's. Replicas now land one per node.
func TestServiceReplicasSpreadAcrossUnequalNodes(t *testing.T) {
	f := newSpreadFleet(t, 80, 4)
	app := f.app(t, api.PlanPro, 256, 2)
	dep, err := f.store.LiveDeployment(f.ctx, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	nodes := map[string]int{}
	for range 2 {
		res, err := f.e.AdmitInstanceForDeployment(f.ctx, app.ID, dep.ID, "", sched.TriggerServiceReplica)
		if err != nil || res.AtCapacity {
			t.Fatalf("admit service replica: %+v, %v", res, err)
		}
		nodes[res.NodeID]++
	}
	if len(nodes) != 2 {
		t.Fatalf("two service replicas landed on %d node(s) (%v), want one per node", len(nodes), nodes)
	}
}
