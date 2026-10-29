// spec: §6.2
// adr: 909 — startup recovery records resident rows past the bare ceiling.
package sched_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// Hunt 18 H18-1: measured admission books a node past its bare ceiling
// (ADR-909). After a schedd restart SeedLedger rebuilt the ledger before any
// node measurement existed, so it checked every resident row against 1x and
// dropped the rest. The app's concurrency then undercounted and the next
// admission booted an instance past max_concurrency (invariant §6.2-1).
func TestSeedLedgerKeepsResidentRowsPastTheBareCeiling(t *testing.T) {
	const ceilingMB, ramMB, replicas = 1024, 248, 6
	f := newFailoverFleet(t, 1, ceilingMB, func(string) error { return nil })
	node := f.nodes[0]
	app := f.app(t, api.PlanPro, ramMB, replicas)
	dep, err := f.store.LiveDeployment(f.ctx, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	// 6 x 256 MB = 1,536 MB: past the 1,024 MB ceiling and inside the 200%
	// bound, booked as a measuring scheduler books them.
	measured := state.WithNodeAdmission(f.ctx, state.NodeAdmission{NodeID: node.ID, Generation: node.LifecycleGeneration,
		ReservationPercent: api.MeasuredAdmissionMaxOvercommitPercent})
	for range replicas {
		if _, err := f.store.CreateInstanceWithMode(measured, app.ID, dep.ID, string(state.StateRunning), ramMB, node.ID, uuid.NewString(), string(state.InstanceModeNormal)); err != nil {
			t.Fatal(err)
		}
	}

	// A restarted schedd seeds before measured admission is switched on
	// (cmd/schedd's order) and before any measurement of the node arrives.
	restarted := sched.XNewEngine(t, f.store, f.vmm, &sched.FakeNotifier{}, "1.10.0").WithOwnerNodeID(node.ID)
	if err := restarted.SeedLedger(f.ctx); err != nil {
		t.Fatal(err)
	}
	if got := restarted.Ledger().Concurrency(app.ID); got != replicas {
		t.Fatalf("seeded concurrency = %d, want all %d resident replicas", got, replicas)
	}
	if got, want := restarted.Ledger().ResidentRAMForNode(node.ID), replicas*(ramMB+api.PerVMOverheadMB); got != want {
		t.Fatalf("seeded node RAM = %d MB, want the full resident %d MB", got, want)
	}
	restarted.Ledger().EnableMeasuredAdmission(func(string) (int, time.Time, bool) { return 64, time.Now(), true })
	restarted.CapacityTable().Replace(sched.CapacityReport{NodeID: node.ID, SampledAt: time.Now(), UsedMB: 64, RAMHeadroomMB: int32(node.MemMB - 64)})

	res, err := restarted.AdmitInstance(f.ctx, app.ID, "", "", sched.TriggerCronSched)
	if err != nil {
		t.Fatalf("AdmitInstance: %v", err)
	}
	if !res.AtCapacity || res.InstanceID != "" {
		t.Fatalf("AdmitInstance = %+v, want at capacity: the app already runs its %d replicas", res, replicas)
	}
}

// Recovery records the full overcommitted RAM and vCPU, as it already did for
// physical CPU, and never authorizes growth: an ordinary admission stays
// refused until the node is back under its budget.
func TestAdmit_ResidentRecoveryAccountsRAMAndVCPUWithoutAllowingGrowth(t *testing.T) {
	t.Parallel()
	request := func(id string) sched.Request {
		return sched.Request{Instance: id, AppID: "app-" + id, Plan: api.PlanScale, RAMMB: 248, VCPU: 2, MaxConcurrency: 1,
			NodeID: "node-a", NodeCeilingMB: 1024, VCPUBudget: 6}
	}
	for _, tc := range []struct {
		name     string
		existing int
		recover  int
	}{
		// 6 x 256 MB = 1,536 MB on a 1,024 MB node; 6 x 2 vCPU = 12 on a 6-vCPU node.
		{name: "ram and vcpu past the budget", existing: 6, recover: 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ledger := sched.NewNodeLedger()
			for i := range tc.existing {
				req := request(fmt.Sprintf("existing-%d", i))
				req.AllowResidentRecovery = true
				if err := ledger.Admit(req); err != nil {
					t.Fatalf("recover existing %d: %v", i, err)
				}
			}
			if got := ledger.ResidentRAMForNode("node-a"); got != tc.existing*256 {
				t.Fatalf("recovered RAM = %d MB, want %d", got, tc.existing*256)
			}
			if err := ledger.Admit(request("new-while-over")); err == nil {
				t.Fatal("an ordinary admission must stay refused while the recovered node is over its budget")
			}
			for i := range tc.recover {
				ledger.Release(fmt.Sprintf("existing-%d", i))
			}
			if err := ledger.Admit(request("new-after-drain")); err != nil {
				t.Fatalf("admission after the node drains under its budget: %v", err)
			}
		})
	}
}
