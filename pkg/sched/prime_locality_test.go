package sched

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPrimePrefersAppNodeWithinCapacity(t *testing.T) {
	for _, tc := range []struct {
		name                       string
		off, full, noCPU, unplaced bool
		wantOwner                  bool
	}{
		{name: "existing owner", wantOwner: true},
		{name: "owner inactive", off: true},
		{name: "owner memory full", full: true},
		{name: "owner CPU full", noCPU: true},
		{name: "unplaced app", unplaced: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			store := state.NewMemStore()
			owner, other := seedTwoNodes(t, store)
			_, app, dep := seedApp(t, store, api.PlanPro, 256, 5)
			if !tc.unplaced {
				if err := store.SetAppNodeID(ctx, app.ID, owner); err != nil {
					t.Fatal(err)
				}
			}
			node, err := store.ComputeNodeByID(ctx, owner)
			if err != nil {
				t.Fatal(err)
			}
			node.AdmissionCeilingMB = 1024
			if tc.noCPU {
				node.VCPUBudget = 1
			}
			if _, err := store.UpsertComputeNode(ctx, node); err != nil {
				t.Fatal(err)
			}
			if tc.off {
				if err := store.SetComputeNodeActive(ctx, owner, false); err != nil {
					t.Fatal(err)
				}
			}
			vmm := &fakeVMM{}
			e := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")
			if tc.full {
				e.capacityTable.Replace(CapacityReport{NodeID: owner, UsedMB: 1024})
			}
			if err := e.Prime(ctx, app.ID, dep.ID); err != nil {
				t.Fatal(err)
			}
			rows, err := store.ListInstancesForApp(ctx, app.ID)
			if err != nil || len(rows) != 1 {
				t.Fatalf("prime rows: %v %v", rows, err)
			}
			want := other
			if tc.wantOwner {
				want = owner
			}
			if rows[0].NodeID != want {
				t.Fatalf("prime node=%s, want %s", rows[0].NodeID, want)
			}
			if vmm.coldBoots != 1 || vmm.snapshots != 1 || rows[0].State != string(state.StateParked) {
				t.Fatalf("prime did not complete boot/capture/park: %+v", rows[0])
			}
		})
	}
}
