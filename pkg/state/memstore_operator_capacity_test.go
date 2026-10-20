// memstore_operator_capacity_test.go — MemStore twin of
// pkg/state/pgstore.go's OperatorCapacity, exercised by the
// /v1/admin/obs/capacity endpoint. OperatorCapacity was added on
// main (post-PR-#1111 rebase) and pulled into this branch with
// zero MemStore test coverage, dropping the pkg/state coverage
// gate from 69.9% → 69.8%. Pinning every conditional branch
// here lifts the gate back above 70%.
//
// Branch coverage:
//
//  1. Baseline: empty store → Nodes empty, AppsTotal=0,
//     TenantsTotal=0, UnplacedApps=0.
//  2. App status filter: AppDeleted apps are excluded from
//     AppsTotal + TenantsTotal.
//  3. Unplaced apps (NodeID == "") count toward AppsTotal +
//     UnplacedApps but not toward any node's AppsCount.
//  4. Per-node AppsCount + TenantsCount (with COUNT(DISTINCT
//     account_id) semantics).
//  5. Instance state buckets: RUNNING / WAKING / COLD_BOOTING
//     each populate the corresponding counter; non-live states
//     (PARKED etc.) are skipped.
//  6. RAMUsedMB arithmetic: RAMMB + 8 per live instance.
//  7. Sort: Active=true first, then Name ascending.
//
// White-box `package state` so the seeder can touch the private
// `apps` / `instances` / `computeNodes` maps directly — no
// public seeding API exists for this shape.
package state_test

import (
	"context"
	"github.com/onebox-faas/faas/pkg/state"
	"testing"
)

func TestMemStore_OperatorCapacity_CoversPgStore(t *testing.T) {
	ctx := context.Background()

	t.Run("empty store", func(t *testing.T) {
		store := state.NewMemStore()
		(*
		// NewMemStore auto-seeds a synthetic 'default-local'
		// node (ADR-025 axis 3, key = newID() UUID with
		// Name="default-local"). Drop it so the baseline test
		// exercises the zero-node branch.
		store.Mu()).Lock()
		for k, n := range *store.ComputeNodes() {
			if n.Name == state.DefaultLocalNodeName {
				delete((*store.ComputeNodes()), k)
			}
		}
		(*store.Mu()).Unlock()

		got, err := store.OperatorCapacity(ctx)
		if err != nil {
			t.Fatalf("OperatorCapacity: %v", err)
		}
		if len(got.Nodes) != 0 {
			t.Errorf("Nodes = %v, want empty", got.Nodes)
		}
		if got.AppsTotal != 0 {
			t.Errorf("AppsTotal = %d, want 0", got.AppsTotal)
		}
		if got.TenantsTotal != 0 {
			t.Errorf("TenantsTotal = %d, want 0", got.TenantsTotal)
		}
		if got.UnplacedApps != 0 {
			t.Errorf("UnplacedApps = %d, want 0", got.UnplacedApps)
		}
	})

	t.Run("placement + tenants + instance buckets", func(t *testing.T) {
		store := state.NewMemStore()
		seedOperatorCapacityFixtures(store)

		got, err := store.OperatorCapacity(ctx)
		if err != nil {
			t.Fatalf("OperatorCapacity: %v", err)
		}

		// 5 live apps (1 deleted excluded).
		if got.AppsTotal != 5 {
			t.Errorf("AppsTotal = %d, want 5", got.AppsTotal)
		}
		// 3 distinct tenants across the live apps (tenant-A and
		// tenant-C each appear twice; COUNT(DISTINCT) collapses
		// {A, B, C} to 3).
		if got.TenantsTotal != 3 {
			t.Errorf("TenantsTotal = %d, want 3", got.TenantsTotal)
		}
		// 1 unplaced app (NodeID == "").
		if got.UnplacedApps != 1 {
			t.Errorf("UnplacedApps = %d, want 1", got.UnplacedApps)
		}

		// Sort: active=true first ("default-local" + "node-1"),
		// then active=false ("node-2") by Name ascending.
		if len(got.Nodes) != 3 {
			t.Fatalf("len(Nodes) = %d, want 3", len(got.Nodes))
		}
		if !got.Nodes[0].Active {
			t.Errorf("Nodes[0].Active = false, want true (active-first sort)")
		}
		if got.Nodes[0].Name != state.DefaultLocalNodeName {
			t.Errorf("Nodes[0].Name = %q, want %q", got.Nodes[0].Name, state.DefaultLocalNodeName)
		}
		if !got.Nodes[1].Active {
			t.Errorf("Nodes[1].Active = false, want true")
		}
		if got.Nodes[1].ID != "node-1" {
			t.Errorf("Nodes[1].ID = %q, want node-1", got.Nodes[1].ID)
		}
		if got.Nodes[2].Active {
			t.Errorf("Nodes[2].Active = true, want false (inactive sorted last)")
		}

		// node-1 (index 1) holds 3 apps (app-1 tenant-A +
		// app-2 tenant-C + app-3 tenant-C — 2 distinct tenants).
		n1 := got.Nodes[1]
		if n1.ID != "node-1" {
			t.Fatalf("Nodes[1].ID = %q, want node-1", n1.ID)
		}
		if n1.AppsCount != 3 {
			t.Errorf("node-1 AppsCount = %d, want 3", n1.AppsCount)
		}
		if n1.TenantsCount != 2 {
			t.Errorf("node-1 TenantsCount = %d, want 2 (A + C)", n1.TenantsCount)
		}
		// node-1 hosts 3 live instances (1 RUNNING + 1 WAKING
		// + 1 COLD_BOOTING); the PARKED inst-4 is filtered.
		if n1.InstancesLive != 3 {
			t.Errorf("node-1 InstancesLive = %d, want 3", n1.InstancesLive)
		}
		if n1.InstancesRunning != 1 {
			t.Errorf("node-1 InstancesRunning = %d, want 1", n1.InstancesRunning)
		}
		if n1.InstancesWaking != 1 {
			t.Errorf("node-1 InstancesWaking = %d, want 1", n1.InstancesWaking)
		}
		if n1.InstancesColdBooting != 1 {
			t.Errorf("node-1 InstancesColdBooting = %d, want 1", n1.InstancesColdBooting)
		}
		// RAM math: (256+8) + (128+8) + (512+8) = 920 MB.
		if n1.RAMUsedMB != 920 {
			t.Errorf("node-1 RAMUsedMB = %d, want 920", n1.RAMUsedMB)
		}

		// node-2 (index 2) hosts 1 app + 0 live instances (the
		// PARKED instance is filtered by isInstanceStateLive).
		n2 := got.Nodes[2]
		if n2.ID != "node-2" {
			t.Fatalf("Nodes[2].ID = %q, want node-2", n2.ID)
		}
		if n2.AppsCount != 1 {
			t.Errorf("node-2 AppsCount = %d, want 1", n2.AppsCount)
		}
		if n2.TenantsCount != 1 {
			t.Errorf("node-2 TenantsCount = %d, want 1", n2.TenantsCount)
		}
		if n2.InstancesLive != 0 {
			t.Errorf("node-2 InstancesLive = %d, want 0 (PARKED filtered)", n2.InstancesLive)
		}
		if n2.RAMUsedMB != 0 {
			t.Errorf("node-2 RAMUsedMB = %d, want 0 (no live instances)", n2.RAMUsedMB)
		}
	})
}

// seedOperatorCapacityFixtures builds a MemStore populated with
// every shape OperatorCapacity's branches care about:
//
//   - 1 compute node active + 1 compute node inactive (sort test)
//   - 1 app with AppDeleted (excluded from counts)
//   - 1 app with NodeID == "" (counts toward AppsTotal + Unplaced)
//   - 2 apps on node-1, owned by 2 distinct tenants (TenantsCount
//     collapses via COUNT(DISTINCT))
//   - 3 instances on node-1 (RUNNING + WAKING + COLD_BOOTING)
//   - 1 instance on node-1 in a non-live state (PARKED — filtered)
//   - 1 instance on node-2 in PARKED (filtered by isInstanceStateLive)
func seedOperatorCapacityFixtures(store *state.MemStore) {
	(*store.ComputeNodes())["node-1"] = state.ComputeNode{
		ID:                 "node-1",
		Name:               "node-1",
		Active:             true,
		VPCPUs:             16,
		VCPUBudget:         12,
		MemMB:              32768,
		AdmissionCeilingMB: 27300,
	}
	(*store.ComputeNodes())["node-2"] = state.ComputeNode{
		ID:                 "node-2",
		Name:               "node-2",
		Active:             false,
		VPCPUs:             8,
		VCPUBudget:         6,
		MemMB:              16384,
		AdmissionCeilingMB: 13650,
	}
	(*

	// App on node-1, tenant-A, with a live RUNNING instance.
	store.Apps())["app-1"] = state.App{
		ID:        "app-1",
		AccountID: "tenant-A",
		NodeID:    "node-1",
		Status:    state.AppActive,
	}
	(*
	// Second app on node-1, tenant-C, with a WAKING instance.
	store.Apps())["app-2"] = state.App{
		ID:        "app-2",
		AccountID: "tenant-C",
		NodeID:    "node-1",
		Status:    state.AppActive,
	}
	(*
	// Third app on node-1, tenant-C again (TenantsCount
	// collapses via DISTINCT).
	store.Apps())["app-3"] = state.App{
		ID:        "app-3",
		AccountID: "tenant-C",
		NodeID:    "node-1",
		Status:    state.AppActive,
	}
	(*
	// Unplaced app (no NodeID) — counts toward AppsTotal +
	// UnplacedApps but NOT toward any node's AppsCount.
	store.Apps())["app-4"] = state.App{
		ID:        "app-4",
		AccountID: "tenant-B",
		NodeID:    "",
		Status:    state.AppActive,
	}
	(*
	// Deleted app — must be excluded from all counts.
	store.Apps())["app-deleted"] = state.App{
		ID:        "app-deleted",
		AccountID: "tenant-X",
		NodeID:    "node-1",
		Status:    state.AppDeleted,
	}
	(*
	// App on node-2 hosting only a PARKED instance (filtered).
	store.Apps())["app-5"] = state.App{
		ID:        "app-5",
		AccountID: "tenant-A",
		NodeID:    "node-2",
		Status:    state.AppActive,
	}
	(*store.Instances())["inst-1"] = state.Instance{
		ID:     "inst-1",
		AppID:  "app-1",
		NodeID: "node-1",
		State:  state.InstanceStateRunning,
		RAMMB:  256,
	}
	(*store.Instances())["inst-2"] = state.Instance{
		ID:     "inst-2",
		AppID:  "app-2",
		NodeID: "node-1",
		State:  state.InstanceStateWaking,
		RAMMB:  128,
	}
	(*store.Instances())["inst-3"] = state.Instance{
		ID:     "inst-3",
		AppID:  "app-3",
		NodeID: "node-1",
		State:  state.InstanceStateColdBooting,
		RAMMB:  512,
	}
	(*
	// PARKED instance on node-1 — filtered out by
	// isInstanceStateLive.
	store.Instances())["inst-4"] = state.Instance{
		ID:     "inst-4",
		AppID:  "app-3",
		NodeID: "node-1",
		State:  "PARKED",
		RAMMB:  999,
	}
	(*
	// PARKED instance on node-2 — filtered out by both
	// isInstanceStateLive and the NodeID lookup loop.
	store.Instances())["inst-5"] = state.Instance{
		ID:     "inst-5",
		AppID:  "app-5",
		NodeID: "node-2",
		State:  "PARKED",
		RAMMB:  256,
	}
}
