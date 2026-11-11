// adr: 20261110210000
package sched_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// A pressured owner under control-plane authority asks; it does not move
// the app. The control plane then moves it off the requester to a peer it
// chooses and consumes the request.
func TestOwnerReleaseRequestMovesAppThroughControlPlane(t *testing.T) {
	store, ctx, owner, peer := rebalanceTestOwners(t)
	app := seedAppOnNode(t, store, ctx, api.PlanHobby, 128, owner.ID)

	node := newRebalanceEngine(t, store, owner.ID, &sched.FakeNotifier{})
	if err := node.RequestPressureRelease(ctx, app.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := store.AppByID(ctx, app.ID); got.NodeID != owner.ID {
		t.Fatalf("the node moved the app itself (owner %s)", got.NodeID)
	}
	reqs, err := store.ListAppOwnerReleaseRequests(ctx, 10)
	if err != nil || len(reqs) != 1 || reqs[0].AppID != app.ID || reqs[0].FromNodeID != owner.ID {
		t.Fatalf("release requests = %+v, %v", reqs, err)
	}

	controlPlane := newRebalanceEngine(t, store, "", &sched.FakeNotifier{})
	n, err := controlPlane.ProcessOwnerReleaseRequests(ctx)
	if err != nil || n != 1 {
		t.Fatalf("processed %d, %v", n, err)
	}
	if got, _ := store.AppByID(ctx, app.ID); got.NodeID != peer.ID {
		t.Fatalf("owner after release = %s, want the peer %s", got.NodeID, peer.ID)
	}
	if reqs, _ := store.ListAppOwnerReleaseRequests(ctx, 10); len(reqs) != 0 {
		t.Fatalf("request not consumed: %+v", reqs)
	}
}

// A node can only ask to release what it owns; a request naming another
// owner (a forged row) is dropped without moving the app.
func TestOwnerReleaseRequestForAnotherOwnerIsDropped(t *testing.T) {
	store, ctx, owner, peer := rebalanceTestOwners(t)
	app := seedAppOnNode(t, store, ctx, api.PlanHobby, 128, owner.ID)

	// The peer's schedd refuses to ask for an app it does not own.
	if err := newRebalanceEngine(t, store, peer.ID, &sched.FakeNotifier{}).RequestPressureRelease(ctx, app.ID); err != nil {
		t.Fatal(err)
	}
	if reqs, _ := store.ListAppOwnerReleaseRequests(ctx, 10); len(reqs) != 0 {
		t.Fatalf("a non-owner filed a request: %+v", reqs)
	}
	// A row written directly with the wrong owner is consumed, not acted on.
	if err := store.RequestAppOwnerRelease(ctx, app.ID, peer.ID); err != nil {
		t.Fatal(err)
	}
	if n, err := newRebalanceEngine(t, store, "", &sched.FakeNotifier{}).ProcessOwnerReleaseRequests(ctx); err != nil || n != 1 {
		t.Fatalf("processed %d, %v", n, err)
	}
	if got, _ := store.AppByID(ctx, app.ID); got.NodeID != owner.ID {
		t.Fatalf("a forged request moved the app to %s", got.NodeID)
	}
}

var _ state.AppOwnerReleaseStore = (*state.MemStore)(nil)
