package conformance

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

// Exercise the same ownership, handoff, and contention contract on real
// PostgreSQL and MemStore. A peer's older request must not block local work.
func testFireNowNodeOwnershipAndHandoff(t *testing.T, fx *Fixture) {
	peer := fx.Node
	peer.ID, peer.Name = "", "cron-peer-"+uuid.NewString()
	peer, err := fx.Store.CreateComputeNode(fx.Ctx, peer)
	if err != nil {
		t.Fatal(err)
	}
	seedRequest := func(nodeID string) string {
		t.Helper()
		app := fx.App
		app.ID, app.Slug, app.NodeID = "", "cron-owner-"+uuid.NewString(), nodeID
		app, err := fx.Store.CreateApp(fx.Ctx, app)
		if err != nil {
			t.Fatal(err)
		}
		cron, err := fx.Store.CreateCron(fx.Ctx, app.ID, "0 0 * * *", "/tick", true)
		if err != nil {
			t.Fatal(err)
		}
		id, err := fx.Store.InsertFireNowRequest(fx.Ctx, cron.ID, fx.Account.ID)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	peerRequest := seedRequest(peer.ID)
	if err := fx.Store.SetAppNodeID(fx.Ctx, fx.App.ID, fx.Node.ID); err != nil {
		t.Fatal(err)
	}
	cron, err := fx.Store.CreateCron(fx.Ctx, fx.App.ID, "0 0 * * *", "/tick", true)
	if err != nil {
		t.Fatal(err)
	}
	localRequest, err := fx.Store.InsertFireNowRequest(fx.Ctx, cron.ID, fx.Account.ID)
	if err != nil {
		t.Fatal(err)
	}
	unassignedRequest := seedRequest("")
	req, err := fx.Store.ClaimPendingFireNowRequestForNode(fx.Ctx, "")
	if err != nil || req.ID != unassignedRequest {
		t.Fatalf("ownerless claim = %s, %v; want only unassigned %s", req.ID, err, unassignedRequest)
	}
	if err := fx.Store.MarkFireNowRequestFailed(fx.Ctx, req.ID, "test terminal row"); err != nil {
		t.Fatal(err)
	}
	if err := fx.Store.RequeueFireNowRequest(fx.Ctx, req.ID); !errors.Is(err, state.ErrFireNowRequestNotFound) {
		t.Fatalf("terminal requeue = %v, want not found", err)
	}
	claimed := claimRace(t, 6, func() (string, bool, error) {
		req, err := fx.Store.ClaimPendingFireNowRequestForNode(fx.Ctx, fx.Node.ID)
		return req.ID, err == nil, err
	})
	if len(claimed) != 1 || claimed[0] != localRequest {
		t.Fatalf("local contenders claimed %v, want [%s]", claimed, localRequest)
	}
	foreign, err := fx.Store.GetFireNowRequest(fx.Ctx, peerRequest)
	if err != nil || foreign.Status != state.FireNowStatusPending {
		t.Fatalf("peer request = %+v, %v; want pending", foreign, err)
	}
	// Move ownership after claim. The request must survive the handoff with
	// its identity and original timestamp, then be claimable by the peer.
	before, err := fx.Store.GetFireNowRequest(fx.Ctx, localRequest)
	if err != nil {
		t.Fatal(err)
	}
	if err := fx.Store.ReassignAppOwner(fx.Ctx, fx.App.ID, fx.Node.ID, peer.ID); err != nil {
		t.Fatal(err)
	}
	if err := fx.Store.RequeueFireNowRequest(fx.Ctx, localRequest); err != nil {
		t.Fatal(err)
	}
	if _, err := fx.Store.ClaimPendingFireNowRequestForNode(fx.Ctx, fx.Node.ID); !errors.Is(err, state.ErrFireNowRequestNotFound) {
		t.Fatalf("old owner claimed after handoff: %v", err)
	}
	req, err = fx.Store.ClaimPendingFireNowRequestForNode(fx.Ctx, peer.ID)
	if err != nil || req.ID != peerRequest {
		t.Fatalf("peer FIFO claim = %s, %v; want older %s", req.ID, err, peerRequest)
	}
	claimed = claimRace(t, 6, func() (string, bool, error) {
		req, err := fx.Store.ClaimPendingFireNowRequestForNode(fx.Ctx, peer.ID)
		return req.ID, err == nil, err
	})
	if len(claimed) != 1 || claimed[0] != localRequest {
		t.Fatalf("new owner contenders claimed %v, want [%s]", claimed, localRequest)
	}
	after, err := fx.Store.GetFireNowRequest(fx.Ctx, localRequest)
	if err != nil || !after.RequestedAt.Equal(before.RequestedAt) || after.Status != state.FireNowStatusRunning {
		t.Fatalf("handoff changed identity/time or lost claim: %+v, %v", after, err)
	}
}
