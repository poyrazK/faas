package conformance

import (
	"sort"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

// testLiveInstancesByNode pins the scheduler's per-tick reader (H10-145):
// the live instances of the node's non-deleted apps, never parked history,
// another node's apps or a deleted app's leftover rows.
func testLiveInstancesByNode(t *testing.T, fx *Fixture) {
	other, err := fx.Store.CreateComputeNode(fx.Ctx, state.ComputeNode{
		Name:               "conformance-other-" + uuid.NewString(),
		TargetURL:          "unix:///tmp/conformance-other-vmmd.sock",
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
	deleted := liveByNodeApp(t, fx, fx.Node.ID)

	var want []string
	for _, st := range []state.State{state.StateRunning, state.StateWaking, state.StateColdBooting} {
		want = append(want, liveByNodeInstance(t, fx, owned, fx.Node.ID, st).ID)
	}
	parked := liveByNodeInstance(t, fx, owned, fx.Node.ID, state.StateRunning)
	if err := fx.Store.UpdateInstanceState(fx.Ctx, parked.ID, string(state.StateParked)); err != nil {
		t.Fatalf("UpdateInstanceState(parked): %v", err)
	}
	liveByNodeInstance(t, fx, foreign, other.ID, state.StateRunning)
	liveByNodeInstance(t, fx, deleted, fx.Node.ID, state.StateRunning)
	if _, err := fx.Store.SoftDeleteAppCascade(fx.Ctx, deleted.AppID); err != nil {
		t.Fatalf("SoftDeleteAppCascade: %v", err)
	}

	rows, err := fx.Store.ListLiveInstancesByNodeID(fx.Ctx, fx.Node.ID)
	if err != nil {
		t.Fatalf("ListLiveInstancesByNodeID: %v", err)
	}
	got := make([]string, 0, len(rows))
	for _, row := range rows {
		if !state.IsLive(row.State) {
			t.Errorf("ListLiveInstancesByNodeID returned %s in state %q", row.ID, row.State)
		}
		got = append(got, row.ID)
	}
	sort.Strings(got)
	sort.Strings(want)
	if len(got) != len(want) {
		t.Fatalf("ListLiveInstancesByNodeID = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ListLiveInstancesByNodeID = %v, want %v", got, want)
		}
	}
}

func liveByNodeApp(t *testing.T, fx *Fixture, nodeID string) state.Deployment {
	t.Helper()
	app, err := fx.Store.CreateApp(fx.Ctx, state.App{
		AccountID:      fx.Account.ID,
		Slug:           "live-by-node-" + uuid.NewString()[:8],
		Type:           state.AppTypeApp,
		Runtime:        "node22",
		RAMMB:          128,
		MaxConcurrency: 5,
		NodeID:         nodeID,
		Status:         state.AppActive,
	})
	if err != nil {
		t.Fatalf("CreateApp(%s): %v", nodeID, err)
	}
	dep, err := fx.Store.CreateDeployment(fx.Ctx, state.Deployment{
		AppID:       app.ID,
		Kind:        state.DeploymentKindImage,
		ImageDigest: "sha256:" + uuid.NewString(),
		Status:      state.DeployPending,
	})
	if err != nil {
		t.Fatalf("CreateDeployment(%s): %v", app.ID, err)
	}
	return dep
}

func liveByNodeInstance(t *testing.T, fx *Fixture, dep state.Deployment, nodeID string, st state.State) state.Instance {
	t.Helper()
	ins, err := fx.Store.CreateInstance(fx.Ctx, dep.AppID, dep.ID, string(st), 128, nodeID, uuid.NewString())
	if err != nil {
		t.Fatalf("CreateInstance(%s): %v", st, err)
	}
	return ins
}
