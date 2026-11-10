package sched_test

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
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
			owner, other := sched.SeedTwoNodes(t, store)
			_, app, dep := sched.SeedApp(t, store, api.PlanPro, 256, 5)
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
			if tc.noCPU {
				fillNodeVCPUBudget(t, store, owner)
			}
			if tc.off {
				if err := store.SetComputeNodeActive(ctx, owner, false); err != nil {
					t.Fatal(err)
				}
			}
			vmm := &sched.FakeVMM{}
			e := sched.XNewEngine(t, store, vmm, &sched.FakeNotifier{}, "1.10.0")
			if tc.full {
				(*e.XCapacityTable()).Replace(sched.CapacityReport{NodeID: owner, UsedMB: 1024})
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
			if (*vmm.ColdBoots()) != 1 || (*vmm.Snapshots()) != 1 || rows[0].State != string(state.StateParked) {
				t.Fatalf("prime did not complete boot/capture/park: %+v", rows[0])
			}
		})
	}
}

// fillNodeVCPUBudget runs an instance of another app on nodeID. Instances
// boot one vCPU per GB (ADR-20261109172421), so a 256 MB app no longer fills a
// one-vCPU budget alone; this one books the node's whole budget of 1.
func fillNodeVCPUBudget(t *testing.T, store *state.MemStore, nodeID string) {
	t.Helper()
	ctx := context.Background()
	acct, err := store.CreateAccount(ctx, "fill-"+nodeID+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	other, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "fill-" + nodeID[:8], RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: other.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:fill", Status: state.DeployLive})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateInstanceWithMode(ctx, other.ID, dep.ID, string(state.StateRunning), 256, nodeID,
		"fill-"+nodeID, string(state.InstanceModeNormal)); err != nil {
		t.Fatal(err)
	}
}
