//go:build !no_pg

package state_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/conformance"
)

func TestPgRegionalBuildDirectWriteGuards(t *testing.T) {
	pool := openMigratedRegionalPool(t)
	store := state.NewPgStore(pool)
	fx := conformance.Seed(t, store)
	region := "eu-builder"
	if _, err := store.UpsertComputeRegion(fx.Ctx, state.ComputeRegion{ID: region, DisplayName: "Builders", AcceptingNewApps: true}); err != nil {
		t.Fatal(err)
	}
	node := fx.Node
	node.Region = &region
	var err error
	node, err = store.UpsertComputeNodeFromOperator(fx.Ctx, node)
	if err != nil {
		t.Fatal(err)
	}
	peer := node
	peer.ID, peer.Name = "", "peer-"+uuid.NewString()
	peer, err = store.CreateComputeNode(fx.Ctx, peer)
	if err != nil {
		t.Fatal(err)
	}
	other, err := store.ComputeNodeByName(fx.Ctx, state.DefaultLocalNodeName)
	if err != nil {
		t.Fatal(err)
	}
	app := fx.App
	app.ID, app.Slug, app.NodeID, app.ComputeRegion = "", "guard-"+uuid.NewString(), "", region
	app, err = store.CreateApp(fx.Ctx, app)
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(fx.Ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindTarball, Status: state.DeployBuilding})
	if err != nil {
		t.Fatal(err)
	}
	b, err := store.CreateBuild(fx.Ctx, dep.ID, dep.Kind, 10, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, nodeID := range []any{nil, other.ID} {
		_, err := pool.Exec(fx.Ctx, "UPDATE builds SET status='running', started_at=now(), builder_node_id=$2 WHERE id=$1", b.ID, nodeID)
		requireRegionConstraint(t, err, "builds_compute_region")
	}
	if err := store.NodeSetLifecycle(fx.Ctx, node.ID, state.NodeLifecycleActive, state.NodeLifecycleDraining); err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(fx.Ctx, "UPDATE builds SET status='running', builder_node_id=$2 WHERE id=$1", b.ID, node.ID)
	requireRegionConstraint(t, err, "builds_compute_region")
	if err := store.NodeSetLifecycle(fx.Ctx, node.ID, state.NodeLifecycleDraining, state.NodeLifecycleActive); err != nil {
		t.Fatal(err)
	}
	claim, err := store.ClaimQueuedBuildWithNodeAffinity(fx.Ctx, b.ID, node.Name, 0)
	if err != nil || claim.BuilderNodeID != node.ID {
		t.Fatalf("valid claim: %+v %v", claim, err)
	}
	_, err = pool.Exec(fx.Ctx, "UPDATE builds SET builder_node_id=$2 WHERE id=$1", b.ID, peer.ID)
	requireRegionConstraint(t, err, "builds_claim_placement_immutable")
	_, err = pool.Exec(fx.Ctx, "UPDATE builds SET deployment_id=$2 WHERE id=$1", b.ID, fx.Deployment.ID)
	requireRegionConstraint(t, err, "builds_claim_placement_immutable")
	if err := store.MarkBuildCancelled(fx.Ctx, b.ID, "test", false, time.Now()); err != nil {
		t.Fatal(err)
	}
	var owner string
	if err := pool.QueryRow(fx.Ctx, "SELECT builder_node_id::text FROM builder_vm_cleanup WHERE build_id=$1", b.ID).Scan(&owner); err != nil || owner != node.ID {
		t.Fatalf("cancellation transaction lost owner: %q %v", owner, err)
	}
	_, err = pool.Exec(fx.Ctx, "UPDATE builder_vm_cleanup SET builder_node_id=$2 WHERE build_id=$1", b.ID, peer.ID)
	requireRegionConstraint(t, err, "builder_vm_cleanup_owner_immutable")
	// A queued row with an outstanding teardown cannot start another VM with
	// the same build ID. This also protects direct SQL from stale cleanup.
	_, err = pool.Exec(fx.Ctx, "UPDATE builds SET status='queued' WHERE id=$1", b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ClaimQueuedBuildWithNodeAffinity(fx.Ctx, b.ID, peer.Name, 0); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("claim with pending old VM: %v", err)
	}
	_, err = pool.Exec(fx.Ctx, "UPDATE builds SET status='running',builder_node_id=$2 WHERE id=$1", b.ID, peer.ID)
	requireRegionConstraint(t, err, "builds_vm_cleanup_pending")
	if got, err := store.ClaimBuildVMCleanupOnNode(fx.Ctx, peer.Name, 10); err != nil || len(got) != 0 {
		t.Fatalf("peer stole queued-build cleanup: %+v %v", got, err)
	}
	cleanup, err := store.ClaimBuildVMCleanupOnNode(fx.Ctx, node.Name, 10)
	if err != nil || len(cleanup) != 1 {
		t.Fatalf("owner cleanup: %+v %v", cleanup, err)
	}
	if err := store.CompleteBuildVMCleanup(fx.Ctx, b.ID, cleanup[0].ClaimToken, nil); err != nil {
		t.Fatal(err)
	}
	claim, err = store.ClaimQueuedBuildWithNodeAffinity(fx.Ctx, b.ID, peer.Name, 0)
	if err != nil || claim.BuilderNodeID != peer.ID {
		t.Fatalf("reclaim after cleanup: %+v %v", claim, err)
	}
}
