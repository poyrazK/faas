//go:build !no_pg

package state_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

// TestPg_LiveStateQueriesSeeLiveInstances pins that every PgStore query
// which filters on the three live instance states actually matches rows.
//
// instances.state has been lowercase-constrained since migration 00001
// (instances_state_check), but three queries were written with UPPERCASE
// literals — 'RUNNING' / 'WAKING' / 'COLD_BOOTING'. They are syntactically
// valid and return successfully; they just can never match a row, so they
// silently returned zero forever:
//
//   - ConcurrencyForDeployment: the per-(app, deployment) live count.
//   - PerNodeLiveStats: the operator per-node instance/RAM breakdown.
//   - OperatorCapacity: the fleet capacity projection.
//
// MemStore implements all three in Go against the lowercase constants, so
// it was correct and the divergence never showed up: there was no PgStore
// test for any of them and no conformance suite running both.
//
// Each assertion below fails on the uppercase form and passes on the
// lowercase one.
func TestPg_LiveStateQueriesSeeLiveInstances(t *testing.T) {
	s, ctx := pgStore(t)
	_, appID, depID := seedLiveDeploy(t, s, ctx)
	nodeID := resolveDefaultLocal(t, ctx, s)

	// One instance in each live state. RAM is distinct per state so the
	// ram_used_mb roll-ups below cannot pass on a coincidence.
	for _, tc := range []struct {
		st    state.State
		ramMB int
	}{
		{state.StateRunning, 512},
		{state.StateWaking, 256},
		{state.StateColdBooting, 128},
	} {
		if _, err := s.CreateInstance(ctx, appID, depID, string(tc.st), tc.ramMB, nodeID, ""); err != nil {
			t.Fatalf("CreateInstance(%s): %v", tc.st, err)
		}
	}
	const wantLive = 3
	const wantRAM = (512 + 8) + (256 + 8) + (128 + 8)

	t.Run("ConcurrencyForDeployment", func(t *testing.T) {
		got, err := s.ConcurrencyForDeployment(ctx, appID, depID)
		if err != nil {
			t.Fatalf("ConcurrencyForDeployment: %v", err)
		}
		if got != wantLive {
			t.Errorf("ConcurrencyForDeployment = %d, want %d (an UPPERCASE state literal matches no row and yields 0)", got, wantLive)
		}
	})

	t.Run("CountLiveInstancesByDeployment", func(t *testing.T) {
		// Already lowercase; asserted alongside so the two per-deployment
		// counters cannot drift apart again.
		got, err := s.CountLiveInstancesByDeployment(ctx, depID)
		if err != nil {
			t.Fatalf("CountLiveInstancesByDeployment: %v", err)
		}
		if got != wantLive {
			t.Errorf("CountLiveInstancesByDeployment = %d, want %d", got, wantLive)
		}
	})

	t.Run("PerNodeLiveStats", func(t *testing.T) {
		rows, err := s.PerNodeLiveStats(ctx)
		if err != nil {
			t.Fatalf("PerNodeLiveStats: %v", err)
		}
		var found bool
		for _, r := range rows {
			if r.NodeName != state.DefaultLocalNodeName {
				continue
			}
			found = true
			if r.InstancesLive != wantLive {
				t.Errorf("InstancesLive = %d, want %d", r.InstancesLive, wantLive)
			}
			if r.InstancesRunning != 1 || r.InstancesWaking != 1 || r.InstancesColdBooting != 1 {
				t.Errorf("per-state counts = running %d / waking %d / cold_booting %d, want 1/1/1",
					r.InstancesRunning, r.InstancesWaking, r.InstancesColdBooting)
			}
			if r.RAMUsedMB != wantRAM {
				t.Errorf("RAMUsedMB = %d, want %d (plan RAM + 8 per live instance)", r.RAMUsedMB, wantRAM)
			}
		}
		if !found {
			t.Errorf("no row for node %q; the operator per-node pane reads empty while %d instances are live",
				state.DefaultLocalNodeName, wantLive)
		}
	})

	t.Run("OperatorCapacity", func(t *testing.T) {
		snap, err := s.OperatorCapacity(ctx)
		if err != nil {
			t.Fatalf("OperatorCapacity: %v", err)
		}
		var found bool
		for _, n := range snap.Nodes {
			if n.Name != state.DefaultLocalNodeName {
				continue
			}
			found = true
			if n.InstancesLive != wantLive {
				t.Errorf("InstancesLive = %d, want %d", n.InstancesLive, wantLive)
			}
			if n.InstancesRunning != 1 || n.InstancesWaking != 1 || n.InstancesColdBooting != 1 {
				t.Errorf("per-state counts = running %d / waking %d / cold_booting %d, want 1/1/1",
					n.InstancesRunning, n.InstancesWaking, n.InstancesColdBooting)
			}
			if n.RAMUsedMB != wantRAM {
				t.Errorf("RAMUsedMB = %d, want %d", n.RAMUsedMB, wantRAM)
			}
		}
		if !found {
			t.Errorf("no capacity row for node %q", state.DefaultLocalNodeName)
		}
	})
}
