package conformance

import (
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

// Owner-scoped scheduler reads must retain every resident state while leaving
// terminal history and another owner's physical placements out of the result.
func testLiveInstanceListOwnership(t *testing.T, fx *Fixture) {
	peer := fx.Node
	peer.ID, peer.Name = "", "live-peer-"+uuid.NewString()
	peer, err := fx.Store.CreateComputeNode(fx.Ctx, peer)
	if err != nil {
		t.Fatal(err)
	}
	if err := fx.Store.SetAppNodeID(fx.Ctx, fx.App.ID, fx.Node.ID); err != nil {
		t.Fatal(err)
	}
	want := make(map[string]string)
	for _, st := range []string{"waking", "cold_booting", "running", "draining", "snapshotting", "migrating", "warm", "frozen"} {
		ins, err := fx.Store.CreateInstance(fx.Ctx, fx.App.ID, fx.Deployment.ID, st, 128, peer.ID, uuid.NewString())
		if err != nil {
			t.Fatalf("CreateInstance(%s): %v", st, err)
		}
		want[ins.ID] = st
	}
	for _, st := range []string{"parked", "stopped", "failed"} {
		if _, err := fx.Store.CreateInstance(fx.Ctx, fx.App.ID, fx.Deployment.ID, st, 128, fx.Node.ID, uuid.NewString()); err != nil {
			t.Fatalf("CreateInstance(%s): %v", st, err)
		}
	}
	// Physical placement alone must not assign scheduler ownership. Include an
	// unassigned app and a peer-owned app physically resident on the local node.
	for _, owner := range []string{"", peer.ID} {
		app := fx.App
		app.ID, app.Slug, app.NodeID = "", "live-owner-"+uuid.NewString(), owner
		app, err := fx.Store.CreateApp(fx.Ctx, app)
		if err != nil {
			t.Fatal(err)
		}
		dep, err := fx.Store.CreateDeployment(fx.Ctx, state.Deployment{
			AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:conformance", Status: state.DeployPending,
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fx.Store.CreateInstance(fx.Ctx, app.ID, dep.ID, "running", 128, fx.Node.ID, uuid.NewString()); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := fx.Store.ListLiveInstancesByNodeID(fx.Ctx, fx.Node.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 8 {
		t.Fatalf("owner live rows = %d, want 8", len(rows))
	}
	seen := make(map[string]bool)
	for _, row := range rows {
		if st, ok := want[row.ID]; !ok || row.State != st || seen[row.ID] {
			t.Errorf("unexpected or duplicate owner row: id=%s state=%s", row.ID, row.State)
		}
		seen[row.ID] = true
	}
	if rows, err := fx.Store.ListLiveInstancesByNodeID(fx.Ctx, uuid.NewString()); err != nil || len(rows) != 0 {
		t.Fatalf("unknown owner = %d rows, %v; want none", len(rows), err)
	}
	if _, err := fx.Store.SoftDeleteAppCascade(fx.Ctx, fx.App.ID); err != nil {
		t.Fatal(err)
	}
	if rows, err := fx.Store.ListLiveInstancesByNodeID(fx.Ctx, fx.Node.ID); err != nil || len(rows) != 0 {
		t.Fatalf("deleted app owner = %d rows, %v; want none", len(rows), err)
	}
}
