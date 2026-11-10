// adr: 20261109172421

package sched_test

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// A wake of an app whose guest vCPU count schedd shapes books that count in
// the node's durable vCPU sum, not its plan's; any other app keeps the plan's
// (ADR-20261109172421 step 5).
func TestShapedAppBooksItsBootedVCPUs(t *testing.T) {
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
		Name: "booked-vcpu", TargetURL: "tcp://10.0.5.3:7000",
		VPCPUs: 80, MemMB: 16384, MaxConcurrency: 50, AdmissionCeilingMB: 8192, VCPUBudget: 640, Active: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	f.nodes = append(f.nodes, node)
	f.e = sched.XNewEngine(t, mem, f.vmm, &sched.FakeNotifier{}, "1.10.0").
		WithOwnerNodeID(node.ID).WithOpsMetrics(f.ops)
	used := func() int64 {
		t.Helper()
		got, err := mem.ComputeNodeUsedVCPUByNode(ctx, []string{node.ID})
		if err != nil {
			t.Fatal(err)
		}
		return got[node.ID]
	}

	shaped := f.app(t, api.PlanPro, 256, 1)
	// MemStore app IDs are not dashed UUIDs, so list every app.
	if err := f.e.SetGuestVCPUFromCPUApps([]string{"*"}); err != nil {
		t.Fatal(err)
	}
	want := f.e.GuestVCPUs(shaped, api.MustLimitsFor(api.PlanPro))
	if want >= api.MustLimitsFor(api.PlanPro).VCPU {
		t.Fatalf("the shaped app boots %d vCPUs, want fewer than the plan's for this test", want)
	}
	if _, err := f.e.Wake(ctx, shaped.ID, "", "", sched.TriggerCronSched); err != nil {
		t.Fatalf("wake shaped: %v", err)
	}
	if got := used(); got != int64(want) {
		t.Fatalf("a shaped app's instance books %d vCPU, want the %d it boots with", got, want)
	}
	if err := f.e.SetGuestVCPUFromCPUApps(nil); err != nil {
		t.Fatal(err)
	}
	if _, err := f.e.Wake(ctx, f.app(t, api.PlanPro, 256, 1).ID, "", "", sched.TriggerCronSched); err != nil {
		t.Fatalf("wake plan app: %v", err)
	}
	if got, pro := used()-int64(want), api.MustLimitsFor(api.PlanPro).VCPU; got != int64(pro) {
		t.Fatalf("a plan app's instance books %d vCPU, want its plan's %d", got, pro)
	}
}
