// adr: 20261109172421

package sched_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

func cpuAdmissionNodes() []state.ComputeNode {
	a, b := sched.Node("a-id", "a", 0, 8000), sched.Node("b-id", "b", 0, 8000)
	a.VCPUBudget, b.VCPUBudget = 24, 24
	a.VPCPUs, b.VPCPUs = 3, 3 // 24,000 millicores of physical CPU budget
	return []state.ComputeNode{a, b}
}

// CPU decides where an instance goes, never whether it runs: a node within
// its CPU budgets is still preferred, a fleet over them is placed on memory
// and counted, and memory still refuses.
func TestPlacementCPUDecidesWhereNotWhether(t *testing.T) {
	nodes := cpuAdmissionNodes()
	scale := sched.Request{RAMMB: 512, VCPU: 4, CPUMillicores: 1000}
	for _, tc := range []struct {
		name             string
		usedMB, usedVCPU map[string]int64
		usedCPU          map[string]int64
		node             string // "" = refused, "any" = placed on either node
		count            float64
		refusalCode      string
	}{
		{
			name:   "a node within its budgets wins",
			usedMB: map[string]int64{}, usedVCPU: map[string]int64{"a-id": 24, "b-id": 4}, usedCPU: map[string]int64{},
			node: "b-id",
		},
		{
			name:   "every node at its vCPU budget",
			usedMB: map[string]int64{"a-id": 2000, "b-id": 3000}, usedVCPU: map[string]int64{"a-id": 24, "b-id": 24}, usedCPU: map[string]int64{},
			node: "any", count: 1,
		},
		{
			name:   "every node at its CPU budget",
			usedMB: map[string]int64{}, usedVCPU: map[string]int64{}, usedCPU: map[string]int64{"a-id": 24000, "b-id": 24000},
			node: "any", count: 1,
		},
		{
			name:   "memory still refuses",
			usedMB: map[string]int64{"a-id": 7900, "b-id": 7900}, usedVCPU: map[string]int64{"a-id": 24, "b-id": 24}, usedCPU: map[string]int64{},
			refusalCode: "node_ram",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, counted, err := sched.XChooseWithCPUAdmission(nodes, tc.usedMB, tc.usedVCPU, tc.usedCPU, scale)
			if tc.node == "" {
				var problem *api.Problem
				if !errors.As(err, &problem) || problem.Code != api.CodeCapacity {
					t.Fatalf("err = %v, want a capacity refusal", err)
				}
				blockers := sched.AdmissionBlockers(err)
				if len(blockers) == 0 || blockers[0].Code != tc.refusalCode {
					t.Fatalf("blockers = %+v, want %s first", blockers, tc.refusalCode)
				}
				return
			}
			if err != nil || (tc.node != "any" && p.NodeID != tc.node) || p.NodeID == "" || counted != tc.count {
				t.Fatalf("placed %q (err %v), counted %v; want %q, %v", p.NodeID, err, counted, tc.node, tc.count)
			}
		})
	}
}

// The ledger's vCPU and CPU headroom checks count what they would have
// refused and admit; its memory check still refuses.
func TestLedgerCountsCPUPastTheBudgets(t *testing.T) {
	l := sched.NewNodeLedger()
	base := sched.Request{Plan: api.PlanScale, RAMMB: 128, VCPU: 4, MaxConcurrency: 1,
		NodeID: "n", NodeCeilingMB: 4000, VCPUBudget: 8, CPUMillicores: 1000, CPUBudgetMillicores: 2000}
	admit := func(id string, r sched.Request) error {
		r.Instance, r.AppID = id, "app-"+id
		return l.Admit(r)
	}
	for _, id := range []string{"1", "2", "3"} {
		if err := admit(id, base); err != nil {
			t.Fatalf("admission %s: %v, want admitted on memory", id, err)
		}
	}
	if v, c := l.XCPUAdmissionWouldRefuse(sched.CPUGateLedgerVCPU), l.XCPUAdmissionWouldRefuse(sched.CPUGateLedgerCPU); v != 1 || c != 1 {
		t.Fatalf("counted: vcpu %v, cpu %v; want 1 and 1", v, c)
	}
	big := base
	big.RAMMB = 4000
	if err := admit("4", big); err == nil || !strings.Contains(err.Error(), "RAM headroom") {
		t.Fatalf("err = %v, want memory to still refuse", err)
	}
}

// End to end, wakes past a node's vCPU and CPU budgets all run: placement,
// the ledger and the durable node reservation check memory alone.
func TestWakesPastTheCPUBudgetsRun(t *testing.T) {
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
	f := &failoverFleet{store: mem, ctx: ctx, vmm: newRefusingVMM(nil), ops: wire.NewOpsMetrics("schedd")}
	node, err := mem.CreateComputeNode(ctx, state.ComputeNode{
		Name: "cpu-full", TargetURL: "tcp://10.0.5.4:7000",
		VPCPUs: 1, MemMB: 16384, MaxConcurrency: 50, AdmissionCeilingMB: 8192, VCPUBudget: 2, Active: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	f.nodes = append(f.nodes, node)
	f.e = sched.XNewEngine(t, mem, f.vmm, &sched.FakeNotifier{}, "1.10.0").
		WithOwnerNodeID(node.ID).WithOpsMetrics(f.ops)
	// Each 2 GB instance boots 2 vCPUs: the second and third are past the
	// node's 2-vCPU budget.
	for i := range 3 {
		app := f.app(t, api.PlanScale, 2048, 1)
		if _, err := f.e.Wake(ctx, app.ID, "", "", sched.TriggerCronSched); err != nil {
			t.Fatalf("wake %d: %v, want it admitted on memory", i, err)
		}
	}
}

// admitPastCPUBudget asserts r is past its node's vCPU or CPU budget (the
// refusal the ledger made before memory became the only capacity limit) and
// that Admit still admits it on memory (ADR-20261109172421).
func admitPastCPUBudget(t *testing.T, l *sched.NodeLedger, r sched.Request, why string) {
	t.Helper()
	if err := l.XCPUOverBudget(r); err == nil {
		t.Fatalf("%s: not past a vCPU or CPU budget", why)
	}
	if err := l.Admit(r); err != nil {
		t.Fatalf("%s: Admit = %v, want admitted on memory", why, err)
	}
}
