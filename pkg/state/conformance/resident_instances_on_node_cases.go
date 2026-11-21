package conformance

import (
	"sort"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

// testResidentInstancesOnNode pins the operator node detail's reader: every
// instance holding a VM on the node by its physical node_id (so one mid-
// migration from another node's app counts), never parked or terminal
// history, and never a VM on another node even when this node owns its app.
func testResidentInstancesOnNode(t *testing.T, fx *Fixture) {
	other, err := fx.Store.CreateComputeNode(fx.Ctx, state.ComputeNode{
		Name:               "conformance-resident-" + uuid.NewString(),
		TargetURL:          "unix:///tmp/conformance-resident-vmmd.sock",
		VPCPUs:             2,
		MemMB:              4096,
		MaxConcurrency:     20,
		AdmissionCeilingMB: 4096,
		VCPUBudget:         2,
		Lifecycle:          state.NodeLifecycleActive,
	})
	if err != nil {
		t.Fatalf("CreateComputeNode(other): %v", err)
	}
	owned := liveByNodeApp(t, fx, fx.Node.ID)
	foreign := liveByNodeApp(t, fx, other.ID)

	var want []string
	for _, st := range state.ResidentStates() {
		want = append(want, liveByNodeInstance(t, fx, owned, fx.Node.ID, st).ID)
	}
	// Physically here, owned elsewhere: a live migration moved the row first.
	want = append(want, liveByNodeInstance(t, fx, foreign, fx.Node.ID, state.StateMigrating).ID)
	for _, st := range []state.State{state.StateParked, state.StateStopped, state.StateFailed} {
		liveByNodeInstance(t, fx, owned, fx.Node.ID, st)
	}
	// Owned here, physically elsewhere.
	liveByNodeInstance(t, fx, owned, other.ID, state.StateRunning)

	rows, err := fx.Store.ListResidentInstancesOnNodeID(fx.Ctx, fx.Node.ID)
	if err != nil {
		t.Fatalf("ListResidentInstancesOnNodeID: %v", err)
	}
	got := make([]string, 0, len(rows))
	for _, row := range rows {
		got = append(got, row.ID)
	}
	sort.Strings(got)
	sort.Strings(want)
	if len(got) != len(want) {
		t.Fatalf("ListResidentInstancesOnNodeID = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ListResidentInstancesOnNodeID = %v, want %v", got, want)
		}
	}
	if rows, err := fx.Store.ListResidentInstancesOnNodeID(fx.Ctx, uuid.NewString()); err != nil || len(rows) != 0 {
		t.Fatalf("ListResidentInstancesOnNodeID(unknown node) = %d rows, %v", len(rows), err)
	}
}
