// adr: 20261121203000
package sched_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// displace moves app off from onto to the way a drain does: from stops
// admitting, ownership recovery hands the app over, and from comes back
// unless stayDown.
func displace(t *testing.T, store *state.MemStore, ctx context.Context, appID, from, to string, stayDown bool) {
	t.Helper()
	if err := store.SetComputeNodeActive(ctx, from, false); err != nil {
		t.Fatal(err)
	}
	if err := store.ReassignOrphanedAppOwner(ctx, appID, from, to, -1); err != nil {
		t.Fatalf("displace %s %s -> %s: %v", appID, from, to, err)
	}
	if !stayDown {
		if err := store.SetComputeNodeActive(ctx, from, true); err != nil {
			t.Fatal(err)
		}
	}
}

// thirdNode adds a third active node with the peer's shape.
func thirdNode(t *testing.T, store *state.MemStore, ctx context.Context) state.ComputeNode {
	t.Helper()
	n, err := store.CreateComputeNode(ctx, state.ComputeNode{
		Name: "fsn-third-" + uuid.NewString()[:8], TargetURL: "tcp://10.0.0.3:7000",
		ScheddTargetURL: ptrStringRebalance("tcp://10.0.0.3:7100"), VPCPUs: 80, MemMB: 28000,
		MaxConcurrency: 20, AdmissionCeilingMB: 23800, VCPUBudget: 80, Active: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func ownerHome(t *testing.T, store *state.MemStore, ctx context.Context, appID string) string {
	t.Helper()
	home, err := store.AppOwnerHome(ctx, appID)
	if err != nil {
		t.Fatal(err)
	}
	return home
}

// TestOwnerHomeRecordsTheNodeBeforeADisplacement: a displacement remembers
// where the app came from, an older home survives a chain of displacements,
// landing back home forgets it, and any other move forgets it too.
func TestOwnerHomeRecordsTheNodeBeforeADisplacement(t *testing.T) {
	const a, b, c = 0, 1, 2
	type step struct {
		from, to int
		parked   bool // a balance or pressure move instead of a displacement
	}
	cases := []struct {
		name     string
		steps    []step
		wantHome int // -1: none
	}{
		{"one drain", []step{{from: a, to: b}}, a},
		{"drained back home", []step{{from: a, to: b}, {from: b, to: a}}, -1},
		{"chain keeps the first home", []step{{from: a, to: b}, {from: b, to: c}}, a},
		{"chain ends home", []step{{from: a, to: b}, {from: b, to: c}, {from: c, to: a}}, -1},
		{"balance move after a drain forgets home", []step{{from: a, to: b}, {from: b, to: c, parked: true}}, -1},
		{"drain after a balance move", []step{{from: a, to: b, parked: true}, {from: b, to: c}}, b},
		{"never displaced", nil, -1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store, ctx, owner, peer := rebalanceTestOwners(t)
			nodes := []string{owner.ID, peer.ID, thirdNode(t, store, ctx).ID}
			app := seedAppOnNode(t, store, ctx, api.PlanHobby, 256, owner.ID)
			for _, s := range tc.steps {
				if s.parked {
					if err := store.ReassignParkedAppOwner(ctx, app.ID, nodes[s.from], nodes[s.to]); err != nil {
						t.Fatal(err)
					}
					continue
				}
				displace(t, store, ctx, app.ID, nodes[s.from], nodes[s.to], false)
			}
			want := ""
			if tc.wantHome >= 0 {
				want = nodes[tc.wantHome]
			}
			if got := ownerHome(t, store, ctx, app.ID); got != want {
				t.Fatalf("home = %q, want %q", got, want)
			}
		})
	}
}

// TestOwnerBalanceReturnsDisplacedAppsHome: an app a drain pushed onto
// default-local goes back to the peer once the peer is a ready, restoring
// destination again, and only while the app is parked.
func TestOwnerBalanceReturnsDisplacedAppsHome(t *testing.T) {
	cases := []struct {
		name     string
		setup    func(t *testing.T, store *state.MemStore, ctx context.Context, app state.App, owner, peer state.ComputeNode)
		homeDown bool
		wantHome bool
	}{
		{name: "parked app returns once home is active", wantHome: true},
		{name: "home still drained", homeDown: true},
		{
			name: "home degraded",
			setup: func(t *testing.T, store *state.MemStore, ctx context.Context, _ state.App, _, peer state.ComputeNode) {
				if _, err := store.SetComputeNodeReadiness(ctx, peer.ID, state.NodeReadinessDegraded, "test"); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "resident instance keeps its owner",
			setup: func(t *testing.T, store *state.MemStore, ctx context.Context, app state.App, owner, _ state.ComputeNode) {
				dep, err := store.CreateDeployment(ctx, state.Deployment{ID: uuid.NewString(), AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:" + app.ID})
				if err != nil {
					t.Fatal(err)
				}
				if _, err := store.CreateInstanceWithMode(ctx, app.ID, dep.ID, string(state.StateRunning), app.RAMMB, owner.ID, uuid.NewString(), string(state.InstanceModeNormal)); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "displaced longer than the window stays",
			setup: func(t *testing.T, store *state.MemStore, ctx context.Context, app state.App, _, _ state.ComputeNode) {
				if err := store.SetAppReassignedAtForTest(ctx, app.ID, time.Now().Add(-api.OwnerReturnHomeWindow-time.Minute)); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "home cannot restore the snapshot",
			setup: func(t *testing.T, store *state.MemStore, ctx context.Context, app state.App, _, _ state.ComputeNode) {
				liveSnapshot(t, store, ctx, app, "T2CL", "Intel(R) Xeon(R) CPU @ 2.20GHz")
			},
		},
		{
			name: "restorable snapshot returns",
			setup: func(t *testing.T, store *state.MemStore, ctx context.Context, app state.App, _, _ state.ComputeNode) {
				liveSnapshot(t, store, ctx, app, "", "")
			},
			wantHome: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store, ctx, owner, peer := rebalanceTestOwners(t)
			app := seedAppOnNode(t, store, ctx, api.PlanScale, 2048, peer.ID)
			displace(t, store, ctx, app.ID, peer.ID, owner.ID, tc.homeDown)
			if tc.setup != nil {
				tc.setup(t, store, ctx, app, owner, peer)
			}
			notif := &sched.FakeNotifier{}
			if _, err := newRebalanceEngine(t, store, "", notif).NewOwnerBalancer().Tick(ctx); err != nil {
				t.Fatal(err)
			}
			got, err := store.AppByID(ctx, app.ID)
			if err != nil {
				t.Fatal(err)
			}
			want := owner.ID
			if tc.wantHome {
				want = peer.ID
			}
			if got.NodeID != want {
				t.Fatalf("owner = %s, want %s (owner %s, home %s)", got.NodeID, want, owner.ID, peer.ID)
			}
			if tc.wantHome && ownerHome(t, store, ctx, app.ID) != "" {
				t.Fatal("home still recorded after the app returned")
			}
		})
	}
}

// TestOwnerBalanceDisplacementDoesNotResetTheIdleGuard: the balance's 24 h
// guard still holds an app that was woken recently or that the balance
// itself moved, but a displacement alone no longer holds an idle app.
func TestOwnerBalanceDisplacementDoesNotResetTheIdleGuard(t *testing.T) {
	cases := []struct {
		name          string
		displaced     bool
		balanceMoved  bool
		recentlyAwake bool
		wantCandidate bool
	}{
		{name: "idle, displaced moments ago", displaced: true, wantCandidate: true},
		{name: "idle, moved by the balance moments ago", balanceMoved: true},
		{name: "woken recently, displaced", displaced: true, recentlyAwake: true},
		{name: "idle, never moved", wantCandidate: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store, ctx, owner, peer := rebalanceTestOwners(t)
			seedOn := peer.ID
			if !tc.displaced && !tc.balanceMoved {
				seedOn = owner.ID
			}
			app := seedAppOnNode(t, store, ctx, api.PlanHobby, 256, seedOn)
			switch {
			case tc.displaced:
				// Stays down so the app is not returned home first.
				displace(t, store, ctx, app.ID, peer.ID, owner.ID, true)
			case tc.balanceMoved:
				if err := store.ReassignParkedAppOwner(ctx, app.ID, peer.ID, owner.ID); err != nil {
					t.Fatal(err)
				}
			}
			if tc.recentlyAwake {
				dep, err := store.CreateDeployment(ctx, state.Deployment{ID: uuid.NewString(), AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:" + app.ID})
				if err != nil {
					t.Fatal(err)
				}
				if _, err := store.CreateInstanceWithMode(ctx, app.ID, dep.ID, string(state.StateParked), app.RAMMB, owner.ID, uuid.NewString(), string(state.InstanceModeNormal)); err != nil {
					t.Fatal(err)
				}
			}
			got, err := store.ListOwnerBalanceCandidates(ctx, owner.ID, time.Now().Add(-api.OwnerBalanceMinIdle), 10)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, c := range got {
				found = found || c.ID == app.ID
			}
			if found != tc.wantCandidate {
				t.Fatalf("candidate = %t, want %t", found, tc.wantCandidate)
			}
		})
	}
}

// TestOwnerBalanceUndoesARollingRelease replays 2026-11-21: default-local
// drains, then the peer, so default-local owns every app and the peer none.
// Once the peer is back, its parked apps return home and ownership is what it
// was before the release; nothing else moves.
func TestOwnerBalanceUndoesARollingRelease(t *testing.T) {
	store, ctx, owner, peer := rebalanceTestOwners(t)
	var ownerApps, peerApps []state.App
	for i := 0; i < 10; i++ {
		ownerApps = append(ownerApps, seedAppOnNode(t, store, ctx, api.PlanScale, 2048, owner.ID))
	}
	for i := 0; i < 25; i++ {
		peerApps = append(peerApps, seedAppOnNode(t, store, ctx, api.PlanHobby, 256, peer.ID))
	}
	for _, a := range ownerApps {
		displace(t, store, ctx, a.ID, owner.ID, peer.ID, false)
	}
	for _, a := range append(append([]state.App{}, ownerApps...), peerApps...) {
		displace(t, store, ctx, a.ID, peer.ID, owner.ID, false)
	}
	all := append(append([]state.App{}, ownerApps...), peerApps...)
	if got := ownersOf(t, store, ctx, all); got[owner.ID] != len(all) {
		t.Fatalf("after the release owners = %v, want all on default-local", got)
	}
	b := newRebalanceEngine(t, store, "", &sched.FakeNotifier{}).NewOwnerBalancer()
	if n, err := b.Tick(ctx); err != nil || n != api.OwnerReturnHomeMovesPerTick {
		t.Fatalf("first tick moved %d, %v; want %d", n, err, api.OwnerReturnHomeMovesPerTick)
	}
	for i := 0; i < 10; i++ {
		if _, err := b.Tick(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if got := ownersOf(t, store, ctx, ownerApps); got[owner.ID] != len(ownerApps) {
		t.Fatalf("default-local's own apps: owners = %v", got)
	}
	if got := ownersOf(t, store, ctx, peerApps); got[peer.ID] != len(peerApps) {
		t.Fatalf("the peer's apps: owners = %v, want all home", got)
	}
	for _, a := range all {
		if home := ownerHome(t, store, ctx, a.ID); home != "" {
			t.Fatalf("app %s still records home %s", a.ID, home)
		}
	}
}
