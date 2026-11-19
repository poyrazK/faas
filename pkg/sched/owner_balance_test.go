// adr: 20261118160000
package sched_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// ownerBalanceFleet is default-local (the default ceiling) and a peer with a
// 23,800 MB ceiling, ownerApps 2 GB apps on default-local and none on the
// peer.
func ownerBalanceFleet(t *testing.T, ownerApps int) (*state.MemStore, context.Context, state.ComputeNode, state.ComputeNode, []state.App) {
	t.Helper()
	store, ctx, owner, peer := rebalanceTestOwners(t)
	var apps []state.App
	for i := 0; i < ownerApps; i++ {
		apps = append(apps, seedAppOnNode(t, store, ctx, api.PlanScale, 2048, owner.ID))
	}
	return store, ctx, owner, peer, apps
}

func ownersOf(t *testing.T, store *state.MemStore, ctx context.Context, apps []state.App) map[string]int {
	t.Helper()
	out := map[string]int{}
	for _, a := range apps {
		got, err := store.AppByID(ctx, a.ID)
		if err != nil {
			t.Fatal(err)
		}
		out[got.NodeID]++
	}
	return out
}

// A drain-sized skew is corrected: idle parked apps move to the least owned
// node until the gap is within the target, and an app moved recently stays.
// Default-local starts at 47% of its ceiling and the peer at 0%; three moves
// bring them to 34% and 25%, inside the 10-point target.
func TestOwnerBalanceMovesIdleAppsUntilWithinTarget(t *testing.T) {
	store, ctx, owner, peer, apps := ownerBalanceFleet(t, 10)
	recent := seedAppOnNode(t, store, ctx, api.PlanScale, 2048, peer.ID)
	if err := store.ReassignParkedAppOwner(ctx, recent.ID, peer.ID, owner.ID); err != nil {
		t.Fatal(err)
	}
	notif := &sched.FakeNotifier{}
	b := newRebalanceEngine(t, store, "", notif).NewOwnerBalancer()
	n, err := b.Tick(ctx)
	if err != nil || n != 3 {
		t.Fatalf("tick moved %d, %v; want 3", n, err)
	}
	if got := ownersOf(t, store, ctx, apps); got[peer.ID] != 3 || got[owner.ID] != 7 {
		t.Fatalf("owners = %v, want 3 on the peer", got)
	}
	if got, _ := store.AppByID(ctx, recent.ID); got.NodeID != owner.ID {
		t.Fatal("moved an app reassigned moments ago")
	}
	if n, err := b.Tick(ctx); err != nil || n != 0 {
		t.Fatalf("second tick moved %d, %v; want none once within the target", n, err)
	}
}

// At most api.OwnerBalanceMovesPerTick apps move per tick.
func TestOwnerBalancePacesMoves(t *testing.T) {
	store, ctx, owner, peer := rebalanceTestOwners(t)
	var apps []state.App
	for i := 0; i < 40; i++ {
		apps = append(apps, seedAppOnNode(t, store, ctx, api.PlanHobby, 256, owner.ID))
	}
	b := newRebalanceEngine(t, store, "", &sched.FakeNotifier{}).NewOwnerBalancer()
	if n, err := b.Tick(ctx); err != nil || n != api.OwnerBalanceMovesPerTick {
		t.Fatalf("tick moved %d, %v; want %d", n, err, api.OwnerBalanceMovesPerTick)
	}
	if got := ownersOf(t, store, ctx, apps); got[peer.ID] != api.OwnerBalanceMovesPerTick {
		t.Fatalf("owners = %v", got)
	}
}

// A gap below the trigger moves nothing: default-local at 17%, peer at 0%.
func TestOwnerBalanceIgnoresSmallGaps(t *testing.T) {
	store, ctx, _, _, apps := ownerBalanceFleet(t, 4)
	b := newRebalanceEngine(t, store, "", &sched.FakeNotifier{}).NewOwnerBalancer()
	if n, err := b.Tick(ctx); err != nil || n != 0 {
		t.Fatalf("tick moved %d, %v; want none below the trigger", n, err)
	}
	_ = apps
}

// An app whose live snapshot the destination cannot restore stays, so the
// balance never turns a restore into a cold boot; one whose snapshot it can
// restore moves.
func TestOwnerBalanceKeepsAppsTheDestinationCannotRestore(t *testing.T) {
	store, ctx, owner, peer, apps := ownerBalanceFleet(t, 0)
	pinned := seedAppOnNode(t, store, ctx, api.PlanScale, 2048, owner.ID)
	portable := seedAppOnNode(t, store, ctx, api.PlanScale, 2048, owner.ID)
	for i := 0; i < 8; i++ {
		apps = append(apps, seedAppOnNode(t, store, ctx, api.PlanScale, 2048, owner.ID))
	}
	liveSnapshot(t, store, ctx, pinned, "T2CL", "Intel(R) Xeon(R) CPU @ 2.20GHz")
	liveSnapshot(t, store, ctx, portable, "", "")
	// Make the other apps recently moved so only the two snapshot apps are
	// candidates.
	for _, a := range apps {
		if err := store.ReassignParkedAppOwner(ctx, a.ID, owner.ID, owner.ID); err != nil {
			t.Fatal(err)
		}
	}
	b := newRebalanceEngine(t, store, "", &sched.FakeNotifier{}).NewOwnerBalancer()
	if _, err := b.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if got, _ := store.AppByID(ctx, pinned.ID); got.NodeID != owner.ID {
		t.Fatal("moved an app whose snapshot the destination cannot restore")
	}
	if got, _ := store.AppByID(ctx, portable.ID); got.NodeID != peer.ID {
		t.Fatalf("restorable app owner = %s, want the peer", got.NodeID)
	}
}

// liveSnapshot gives app a live deployment with a stored snapshot captured on
// the given CPU identity.
func liveSnapshot(t *testing.T, store *state.MemStore, ctx context.Context, app state.App, template, model string) {
	t.Helper()
	dep, err := store.CreateDeployment(ctx, state.Deployment{
		ID: uuid.NewString(), AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:" + app.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateDeploymentStatus(ctx, dep.ID, state.DeployLive, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateSnapshot(ctx, state.Snapshot{
		DeploymentID: dep.ID, FCVersion: "fc", StorageKey: "snap/" + dep.ID + "/captures/c/v2/mem",
		CPUTemplate: template, CPUModel: model,
	}); err != nil {
		t.Fatal(err)
	}
}
