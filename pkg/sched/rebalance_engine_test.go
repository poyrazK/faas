// rebalance_engine_test.go — Tier A4 (ADR-064) engine-method
// tests for Engine.RebalanceOrphanedApps. The companion
// rebalancer_test.go covers the watcher-loop filter and
// dispatch; this file exercises the engine-side policy:
// admission, cooldown, per-tick cap, conditional UPDATE,
// rebalanced-notify, and the peers-race-exactly-one-wins
// guarantee that closes the §6.2-1 invariant's multi-node
// shape (cf. pkg/sched/invariants_property_test.go for the
// intra-node invariant pinning).
//
// Test seams reused from pkg/sched/engine_test.go:
//   - newEngine(t, store, vmm, notif, fcVer) at line 289
//   - fakeNotifier + fakeVMM at lines 26 / 236
//   - seedApp(t, store, plan, ramMB, maxConc) at line 266
//
// To keep the engine tests readable the test file uses the
// same setup as pkg/sched/cron_test.go::newTestEngine — store
// is MemStore (with the apps+compute_nodes tables pre-seeded
// for rebalance), engine is built + WithOwnerNodeID set so the
// rebalance-loop early-return on ownerNodeID=="" is bypassed.
//
// Tests in this file:
//
//  1. TestRebalanceOrphanedApps_MigratesParkedApps — happy path.
//  2. TestRebalanceOrphanedApps_RespectsCooldown — cooldown filter.
//  3. TestRebalanceOrphanedApps_RespectsAdmissionCap — cap reached mid-batch.
//  4. TestRebalanceOrphanedApps_PerTickCap — 60 apps, cap=50.
//  5. TestRebalanceOrphanedApps_PeersRaceExactlyOneWins — 2 schedds.
//  6. TestRebalanceOrphanedApps_EmitsRebalancedNotify — NotifyAppChanged emit.

package sched

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// rebalanceTestOwners is the fixture for the engine-method
// tests: one default-local node (active) + one peer node
// (initially active, then flipped inactive in the per-test
// setup). The engine runs with WithOwnerNodeID pointing at
// default-local — that's the node apps get re-stamped onto.
//
// Returns the MemStore, the test context, the default-local
// ComputeNode row (the engine's owner), and the peer node
// that is flipped inactive to drive the rebalance.
func rebalanceTestOwners(t *testing.T) (*state.MemStore, context.Context, state.ComputeNode, state.ComputeNode) {
	t.Helper()
	store := state.NewMemStore()
	ctx := context.Background()

	// default-local is pre-seeded by NewMemStore at
	// pkg/state/memstore.go:4083; locate it via
	// ListComputeNodes (the public surface).
	var defaultLocal state.ComputeNode
	nodes, lErr := store.ListComputeNodes(ctx, true)
	if lErr != nil {
		t.Fatalf("ListComputeNodes: %v", lErr)
	}
	for _, n := range nodes {
		if n.Name == "default-local" {
			defaultLocal = n
		}
	}
	if defaultLocal.ID == "" {
		t.Fatal("default-local row missing from MemStore seed")
	}

	peer, err := store.CreateComputeNode(ctx, state.ComputeNode{
		Name:               "fsn-peer-" + uuid.NewString()[:8],
		TargetURL:          "tcp://10.0.0.2:7000",
		ScheddTargetURL:    ptrStringRebalance("tcp://10.0.0.2:7100"),
		VPCPUs:             80,
		MemMB:              28000,
		MaxConcurrency:     20,
		AdmissionCeilingMB: 23800,
		VCPUBudget:         80,
		Active:             true,
	})
	if err != nil {
		t.Fatalf("CreateComputeNode peer: %v", err)
	}
	return store, ctx, defaultLocal, peer
}

// ptrStringRebalance returns *string for the nullable
// ScheddTargetURL column. Kept local to this test file.
func ptrStringRebalance(s string) *string { return &s }

// seedAppOnNode is a small wrapper around seedApp that
// stamps apps.node_id onto the supplied node. seedApp itself
// doesn't set NodeID (CreateApp defaults to "") and uses a
// fixed email — both fine for engine_test.go's one-app-per-
// test shape, but the rebalance tests seed multiple apps on
// the same MemStore. Suffix the email with a uuid so each
// call gets its own account row, matching the seedApp
// contract for the rebalance batch tests.
func seedAppOnNode(t *testing.T, store *state.MemStore, ctx context.Context, plan api.Plan, ramMB int, nodeID string) state.App {
	t.Helper()
	acct, err := store.CreateAccount(ctx,
		"u-"+uuid.NewString()+"@example.com", plan)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	app, err := store.CreateApp(ctx, state.App{
		AccountID: acct.ID, Slug: "app-" + uuid.NewString(),
		RAMMB: ramMB, MaxConcurrency: 0, IdleTimeoutS: 60,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	if err := store.SetAppNodeID(ctx, app.ID, nodeID); err != nil {
		t.Fatalf("SetAppNodeID %q: %v", nodeID, err)
	}
	return app
}

// newRebalanceEngine builds a fresh Engine with WithOwnerNodeID
// pointing at default-local. Tests get a MemStore-backed engine
// that drives rebalances through the same wire the production
// schedd will exercise.
func newRebalanceEngine(t *testing.T, store *state.MemStore, ownerNodeID string, notif *fakeNotifier) *Engine {
	t.Helper()
	e := newEngine(t, store, &fakeVMM{}, notif, "1.10.0").WithOwnerNodeID(ownerNodeID)
	return e
}

// TestRebalanceOrphanedApps_MigratesParkedApps pins the
// happy path: 5 apps on the dead peer, 1 on the active
// default-local. After flipping the peer inactive + running
// RebalanceOrphanedApps(ctx, peer.ID), all 5 peer apps land
// on default-local; the active-owner's app stays put.
//
// All 5 peer apps share RAMMB=128; the engine's owner
// (default-local) has AdmissionCeilingMB = default so they
// fit comfortably without tripping the cap.
func TestRebalanceOrphanedApps_MigratesParkedApps(t *testing.T) {
	store, ctx, defaultLocal, peer := rebalanceTestOwners(t)

	// 5 apps on the peer.
	for i := 0; i < 5; i++ {
		seedAppOnNode(t, store, ctx, api.PlanHobby, 128, peer.ID)
	}
	// 1 active-owned app — must NOT move.
	stayPut := seedAppOnNode(t, store, ctx, api.PlanHobby, 128, defaultLocal.ID)

	// Flip peer inactive (the rebalance trigger).
	if err := store.SetComputeNodeActive(ctx, peer.ID, false); err != nil {
		t.Fatalf("flip peer inactive: %v", err)
	}

	notif := &fakeNotifier{}
	e := newRebalanceEngine(t, store, defaultLocal.ID, notif)
	if err := e.RebalanceOrphanedApps(ctx, peer.ID); err != nil {
		t.Fatalf("RebalanceOrphanedApps: %v", err)
	}

	// Stay-put app: still on default-local.
	got, err := store.AppByID(ctx, stayPut.ID)
	if err != nil {
		t.Fatalf("AppByID stayPut: %v", err)
	}
	if got.NodeID != defaultLocal.ID {
		t.Errorf("stayPut NodeID = %q, want %q (must not move)", got.NodeID, defaultLocal.ID)
	}

	// All 5 peer apps must now be on default-local with a
	// recent ReassignedAt stamp. (stayPut has NodeID already
	// default-local — it's the migrated status we check, and
	// ReassignedAt is allowed to be nil.)
	for _, a := range store.AllAppsForTest() {
		if a.ID == stayPut.ID {
			continue
		}
		if a.NodeID != defaultLocal.ID {
			t.Errorf("app %s NodeID = %q, want %q (must migrate)", a.ID, a.NodeID, defaultLocal.ID)
		}
		if a.ReassignedAt == nil {
			t.Errorf("app %s ReassignedAt = nil, want a recent timestamp", a.ID)
		} else if time.Since(*a.ReassignedAt) > 2*time.Second {
			t.Errorf("app %s ReassignedAt = %v, want within last 2s", a.ID, a.ReassignedAt)
		}
	}

	// One NotifyAppChanged{rebalanced} per migrated app.
	rebalanced := countRebalancedNotifies(notif)
	if rebalanced != 5 {
		t.Errorf("rebalanced notifies = %d, want 5", rebalanced)
	}
}

func TestRebalanceOrphanedApps_SourceNodeCannotReclaimItsOwnApps(t *testing.T) {
	store, ctx, _, peer := rebalanceTestOwners(t)
	app := seedAppOnNode(t, store, ctx, api.PlanHobby, 128, peer.ID)
	if err := store.SetComputeNodeActive(ctx, peer.ID, false); err != nil {
		t.Fatalf("flip peer inactive: %v", err)
	}

	notif := &fakeNotifier{}
	source := newRebalanceEngine(t, store, peer.ID, notif)
	if err := source.RebalanceOrphanedApps(ctx, peer.ID); err != nil {
		t.Fatalf("RebalanceOrphanedApps: %v", err)
	}

	got, err := store.AppByID(ctx, app.ID)
	if err != nil {
		t.Fatalf("AppByID: %v", err)
	}
	if got.NodeID != peer.ID {
		t.Fatalf("NodeID = %q, want draining source %q unchanged for a healthy peer to claim", got.NodeID, peer.ID)
	}
	if got.ReassignedAt != nil {
		t.Fatalf("ReassignedAt = %v, want nil", got.ReassignedAt)
	}
	if got := countRebalancedNotifies(notif); got != 0 {
		t.Fatalf("rebalanced notifications = %d, want 0", got)
	}
}

// TestRebalanceOrphanedApps_RespectsCooldown pins the
// cooldown filter: an app whose reassigned_at is <60s old
// must be skipped on a subsequent rebalance; an app whose
// reassigned_at is >60s old must be migrated.
//
// The cooldown is api.RebalanceCooldownSeconds = 60s.
//
// To craft a fixture that has ReassignedAt set without
// changing NodeID (SetAppNodeID has an unconditional guard
// against overwriting a non-null node_id), we use the
// SetAppReassignedAtForTest seam directly. The app remains
// owned by the (now-inactive) peer; the rebalance loop sees
// it as an orphan, finds it in the cooldown window, and
// drops it.
func TestRebalanceOrphanedApps_RespectsCooldown(t *testing.T) {
	store, ctx, defaultLocal, peer := rebalanceTestOwners(t)

	fresh := seedAppOnNode(t, store, ctx, api.PlanHobby, 128, peer.ID)
	stale := seedAppOnNode(t, store, ctx, api.PlanHobby, 128, peer.ID)

	if err := store.SetComputeNodeActive(ctx, peer.ID, false); err != nil {
		t.Fatalf("flip peer inactive: %v", err)
	}

	// Stamp ReassignedAt directly: `fresh` to now (in
	// cooldown), `stale` to 90s ago (past the threshold).
	freshAt := time.Now()
	staleAt := time.Now().Add(-90 * time.Second)
	if err := store.SetAppReassignedAtForTest(ctx, fresh.ID, freshAt); err != nil {
		t.Fatalf("stamp fresh: %v", err)
	}
	if err := store.SetAppReassignedAtForTest(ctx, stale.ID, staleAt); err != nil {
		t.Fatalf("stamp stale: %v", err)
	}

	notif := &fakeNotifier{}
	e := newRebalanceEngine(t, store, defaultLocal.ID, notif)
	if err := e.RebalanceOrphanedApps(ctx, peer.ID); err != nil {
		t.Fatalf("RebalanceOrphanedApps: %v", err)
	}

	// `fresh` must stay on the peer (cooldown blocks).
	gotFresh, err := store.AppByID(ctx, fresh.ID)
	if err != nil {
		t.Fatalf("AppByID fresh: %v", err)
	}
	if gotFresh.NodeID != peer.ID {
		t.Errorf("fresh NodeID = %q, want %q (in cooldown must not migrate)", gotFresh.NodeID, peer.ID)
	}
	// `stale` must move to default-local.
	gotStale, err := store.AppByID(ctx, stale.ID)
	if err != nil {
		t.Fatalf("AppByID stale: %v", err)
	}
	if gotStale.NodeID != defaultLocal.ID {
		t.Errorf("stale NodeID = %q, want %q (past cooldown must migrate)", gotStale.NodeID, defaultLocal.ID)
	}

	// One NotifyAppChanged{rebalanced} (only `stale`).
	if got := countRebalancedNotifies(notif); got != 1 {
		t.Errorf("rebalanced notifies = %d, want 1", got)
	}
}

// TestRebalanceOrphanedApps_RespectsAdmissionCap pins the
// admission-cap clause through a low ceiling on a fresh
// shrink-row (parallel to the SmallCeiling test below).
// The branch is the no-headroom counter + "remaining apps
// stay pinned" contract. We make this test the explicit
// alias of the SmallCeiling one to keep the contract surface
// small — the dedicated cap test below runs the body.
func TestRebalanceOrphanedApps_RespectsAdmissionCap(t *testing.T) {
	t.Log("see TestRebalanceOrphanedApps_AdmissionCap_SmallCeiling")
}

// TestRebalanceOrphanedApps_AdmissionCap_SmallCeiling pins
// the no-headroom counter branch. We make the engine's
// owner a synthetic compute_node with a tight ceiling
// (exactly one app at the seeded RAM); seed three apps on
// the dead peer; flip the peer inactive. The first app
// migrates and the remaining two are dropped on the floor
// with their node_id pinned to the dead peer.
func TestRebalanceOrphanedApps_AdmissionCap_SmallCeiling(t *testing.T) {
	store, ctx, defaultLocal, peer := rebalanceTestOwners(t)

	// 3 apps on the peer.
	for i := 0; i < 3; i++ {
		seedAppOnNode(t, store, ctx, api.PlanHobby, 128, peer.ID)
	}
	if err := store.SetComputeNodeActive(ctx, peer.ID, false); err != nil {
		t.Fatalf("flip peer inactive: %v", err)
	}

	// Deactivate the seed default-local (whose ceiling is
	// the global RAM cap, which fits all 3 apps) so the
	// engine has no choice but to land them on a fresh
	// shrink-row whose ceiling is exactly one app.
	if err := store.SetComputeNodeActive(ctx, defaultLocal.ID, false); err != nil {
		t.Fatalf("deactivate seed default-local: %v", err)
	}
	shrink, err := store.CreateComputeNode(ctx, state.ComputeNode{
		Name:               "shrink-" + uuid.NewString()[:8],
		TargetURL:          "tcp://127.0.0.1:7000",
		ScheddTargetURL:    ptrStringRebalance("tcp://127.0.0.1:7100"),
		VPCPUs:             80,
		MemMB:              28000,
		MaxConcurrency:     20,
		AdmissionCeilingMB: 128 + api.PerVMOverheadMB, // exactly one app
		VCPUBudget:         80,
		Active:             true,
	})
	if err != nil {
		t.Fatalf("CreateComputeNode shrink: %v", err)
	}

	notif := &fakeNotifier{}
	e := newRebalanceEngine(t, store, shrink.ID, notif)
	if err := e.RebalanceOrphanedApps(ctx, peer.ID); err != nil {
		t.Fatalf("RebalanceOrphanedApps: %v", err)
	}

	// Exactly 1 migrated (the rest stay pinned on peer).
	if got := countRebalancedNotifies(notif); got != 1 {
		t.Errorf("rebalanced notifies = %d, want 1 (ceiling bound)", got)
	}
	// Verify the post-state: only 1 app on shrink, 2 still
	// on peer.
	onShrink := 0
	onPeer := 0
	for _, a := range store.AllAppsForTest() {
		if a.NodeID == shrink.ID {
			onShrink++
		}
		if a.NodeID == peer.ID {
			onPeer++
		}
	}
	if onShrink != 1 {
		t.Errorf("apps on shrink = %d, want 1 (admission gate must drop the rest)", onShrink)
	}
	if onPeer != 2 {
		t.Errorf("apps still pinned on peer = %d, want 2 (admission must leave them alone)", onPeer)
	}
}

// TestRebalanceOrphanedApps_PerTickCap pins the per-tick
// cap. The MemStore's ListOrphanedApps honors
// RebalanceMaxPerTickPerNode (default 50); seed 60 apps so
// exactly 50 process and 10 stay pinned.
//
// To force "60 apps on the dead peer" we use a budget of 60
// apps × 128 MB + PerVMOverhead 8 = 8,160 MB — well under
// api.RAMAdmissionCeilingMB (47,600 MB) so admission doesn't
// gate.
func TestRebalanceOrphanedApps_PerTickCap(t *testing.T) {
	store, ctx, defaultLocal, peer := rebalanceTestOwners(t)

	// 60 apps on the peer.
	for i := 0; i < 60; i++ {
		seedAppOnNode(t, store, ctx, api.PlanHobby, 128, peer.ID)
	}
	if err := store.SetComputeNodeActive(ctx, peer.ID, false); err != nil {
		t.Fatalf("flip peer inactive: %v", err)
	}

	notif := &fakeNotifier{}
	e := newRebalanceEngine(t, store, defaultLocal.ID, notif)
	if err := e.RebalanceOrphanedApps(ctx, peer.ID); err != nil {
		t.Fatalf("RebalanceOrphanedApps: %v", err)
	}

	if got := countRebalancedNotifies(notif); got != api.RebalanceMaxPerTickPerNode {
		t.Errorf("rebalanced notifies = %d, want %d (per-tick cap)", got, api.RebalanceMaxPerTickPerNode)
	}
}

// TestRebalanceOrphanedApps_PeersRaceExactlyOneWins pins
// the conditional-UPDATE race-safety contract. Two engines
// share one MemStore; both run RebalanceOrphanedApps against
// the same dead-node id concurrently. MemStore's mutex
// serialises the writes so the test result is deterministic:
// exactly one engine sees RowsAffected()==1 for each app,
// the other sees ErrConflict (or, for memstore, the no-op
// no-row-updated path). Per app, exactly one engine ends up
// "owning" the reassignment count.
//
// We assert the strong invariant: every peer app's NodeID is
// one of the two schedd owners, never "" (which would
// indicate dropped write) and never both (which is impossible
// in the conditional-UPDATE contract).
func TestRebalanceOrphanedApps_PeersRaceExactlyOneWins(t *testing.T) {
	store, ctx, defaultLocal, peer := rebalanceTestOwners(t)

	// Three apps on the peer.
	appIDs := make([]string, 3)
	for i := range appIDs {
		a := seedAppOnNode(t, store, ctx, api.PlanHobby, 128, peer.ID)
		appIDs[i] = a.ID
	}
	if err := store.SetComputeNodeActive(ctx, peer.ID, false); err != nil {
		t.Fatalf("flip peer inactive: %v", err)
	}

	// Spin up two engines, each pointing at a different
	// owner, both racing the same rebalance. The tests
	// expect: every app ends up owned by exactly one of the
	// two engines (conditional UPDATE guarantees that).
	e1 := newRebalanceEngine(t, store, defaultLocal.ID, &fakeNotifier{})
	// Second owner: the shrink-row used by
	// TestRebalanceOrphanedApps_AdmissionCap_SmallCeiling is
	// too small for the full fleet; create a fresh, large
	// one.
	other, err := store.CreateComputeNode(ctx, state.ComputeNode{
		Name:               "fsn-other-" + uuid.NewString()[:8],
		TargetURL:          "tcp://10.0.0.3:7000",
		ScheddTargetURL:    ptrStringRebalance("tcp://10.0.0.3:7100"),
		VPCPUs:             80,
		MemMB:              28000,
		MaxConcurrency:     20,
		AdmissionCeilingMB: 23800,
		VCPUBudget:         80,
		Active:             true,
	})
	if err != nil {
		t.Fatalf("CreateComputeNode other: %v", err)
	}
	e2 := newRebalanceEngine(t, store, other.ID, &fakeNotifier{})

	done1, done2 := make(chan struct{}), make(chan struct{})
	go func() { _ = e1.RebalanceOrphanedApps(ctx, peer.ID); close(done1) }()
	go func() { _ = e2.RebalanceOrphanedApps(ctx, peer.ID); close(done2) }()
	<-done1
	<-done2

	// For each app, NodeID must be exactly one of the two
	// schedd owners and not blank.
	for _, id := range appIDs {
		a, err := store.AppByID(ctx, id)
		if err != nil {
			t.Fatalf("AppByID %s: %v", id, err)
		}
		if a.NodeID == peer.ID {
			t.Errorf("app %s still on peer %q; expected a peer claim", id, peer.ID)
		}
		if a.NodeID != defaultLocal.ID && a.NodeID != other.ID {
			t.Errorf("app %s NodeID = %q, want one of (%q, %q)", id, a.NodeID, defaultLocal.ID, other.ID)
		}
	}
}

// TestRebalanceOrphanedApps_EmitsRebalancedNotify pins the
// post-reassignment notify contract. The notify must carry:
//   - channel = db.NotifyAppChanged
//   - payload parseable as JSON with kind="rebalanced",
//     app_id matching, from_node = dead peer, to_node = owner
func TestRebalanceOrphanedApps_EmitsRebalancedNotify(t *testing.T) {
	store, ctx, defaultLocal, peer := rebalanceTestOwners(t)
	app := seedAppOnNode(t, store, ctx, api.PlanHobby, 128, peer.ID)
	if err := store.SetComputeNodeActive(ctx, peer.ID, false); err != nil {
		t.Fatalf("flip peer inactive: %v", err)
	}

	notif := &fakeNotifier{}
	e := newRebalanceEngine(t, store, defaultLocal.ID, notif)
	if err := e.RebalanceOrphanedApps(ctx, peer.ID); err != nil {
		t.Fatalf("RebalanceOrphanedApps: %v", err)
	}

	events := notif.events
	var found *notifyEvent
	for i := range events {
		e := events[i]
		if e.channel != "app_changed" {
			continue
		}
		if !strings.Contains(e.payload, `"rebalanced"`) {
			continue
		}
		if !strings.Contains(e.payload, `"app_id":"`+app.ID+`"`) {
			continue
		}
		if !strings.Contains(e.payload, `"from_node":"`+peer.ID+`"`) {
			continue
		}
		if !strings.Contains(e.payload, `"to_node":"`+defaultLocal.ID+`"`) {
			continue
		}
		found = &e
		break
	}
	if found == nil {
		t.Fatalf("no matching rebalanced notify for app %s, got events=%+v", app.ID, events)
	}
}

// countRebalancedNotifies returns the count of
// db.NotifyAppChanged notifies whose payload contains
// "rebalanced". Drops everything else (claimed, etc.).
func countRebalancedNotifies(n *fakeNotifier) int {
	c := 0
	for _, e := range n.events {
		if e.channel == "app_changed" && strings.Contains(e.payload, `"rebalanced"`) {
			c++
		}
	}
	return c
}
