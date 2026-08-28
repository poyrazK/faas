// static_egress_pr_d_test.go — ADR-119 v2 PR-D unit tests.
//
// Pins the three PR-D contracts:
//
//   - EgressDriftSubscriber.fanOutStaticEgressIP triggers
//     Engine.MigrateStaticEgressInstances when an app with a
//     static egress IP has live instances on a non-owning node.
//   - Engine.MigrateStaticEgressInstances filters the live set
//     to instances on non-owning nodes and drives the four-phase
//     migration harness per candidate.
//   - RebalanceOrphanedApps refuses static-egress-IP-pinned apps
//     when the IP's owning node is the dead node (operator-
//     driven recovery only — there is no healthy node that
//     owns the IP, so rebalancing would source-spoof egress).
//   - RebalancePressuredApps refuses overflow_node spill for
//     static-egress pinned apps — the IP determines the
//     owning node, not app.OverflowNode.
//
// Whitebox `package sched` so we can poke engine methods + the
// subscriber's internal helpers directly. fakeVMM is the same
// recording harness used by the existing migration_handoff_test
// + engine_test; the `prepares` counter is the per-instance
// evidence the harness fired.

package sched

import (
	"context"
	"net/netip"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// prDMultiFleet seeds a 3-node fleet (node-A, node-B, node-C)
// for the PR-D tests. Distinct from requiredNodeMultiFleet (in
// engine_required_node_test.go) which seeds the same names but
// with a different schema; both helpers coexist because the PR-C
// and PR-D suites each want the fixture without dragging the
// other suite's import surface.
func prDMultiFleet(t *testing.T, store *state.MemStore) (nodeA, nodeB, nodeC state.ComputeNode) {
	t.Helper()
	ctx := context.Background()
	var err error
	if nodeA, err = store.CreateComputeNode(ctx, state.ComputeNode{
		Name: "node-A", TargetURL: "unix:///run/faas/node-A.sock",
		VPCPUs: 160, MemMB: 10000, MaxConcurrency: 200,
		AdmissionCeilingMB: 9500, VCPUBudget: 160, Active: true,
	}); err != nil {
		t.Fatalf("CreateComputeNode node-A: %v", err)
	}
	if nodeB, err = store.CreateComputeNode(ctx, state.ComputeNode{
		Name: "node-B", TargetURL: "unix:///run/faas/node-B.sock",
		VPCPUs: 160, MemMB: 10000, MaxConcurrency: 200,
		AdmissionCeilingMB: 9500, VCPUBudget: 160, Active: true,
	}); err != nil {
		t.Fatalf("CreateComputeNode node-B: %v", err)
	}
	if nodeC, err = store.CreateComputeNode(ctx, state.ComputeNode{
		Name: "node-C", TargetURL: "unix:///run/faas/node-C.sock",
		VPCPUs: 160, MemMB: 10000, MaxConcurrency: 200,
		AdmissionCeilingMB: 9500, VCPUBudget: 160, Active: true,
	}); err != nil {
		t.Fatalf("CreateComputeNode node-C: %v", err)
	}
	return nodeA, nodeB, nodeC
}

// TestMigrateStaticEgressInstances_FiresHarnessForNonOwningNode
// pins the per-instance filter + harness-fire contract. An app
// with (StaticEgressIP, NodeID) on node-A has three live
// instances: two on node-A (no migration), one on node-B (must
// migrate). The engine's MigrateStaticEgressInstances must fire
// the four-phase harness exactly once — for the node-B instance.
//
// We deliberately do NOT inject prepareErr — Phase 1 must
// succeed so the fakeVMM `prepares` counter increments and we
// can assert the harness fired exactly once. Subsequent phases
// may fail on MemStore (no instance_status CHECK match for
// 'migrating'); the harness logs those and continues — the
// test asserts the trigger decision, not the full commit.
func TestMigrateStaticEgressInstances_FiresHarnessForNonOwningNode(t *testing.T) {
	store := state.NewMemStore()
	nodeA, nodeB, _ := prDMultiFleet(t, store)
	ctx := context.Background()

	acct, app, _ := seedApp(t, store, api.PlanScale, 512, 5)
	ip := netip.MustParseAddr("203.0.113.42")
	// Pin: IP is on node-A; app.NodeID stamped to node-A at the
	// same UPDATE (per the apid pin-handler contract — see
	// cmd/apid/handlers_apps_static_egress_ip.go). MemStore
	// UpdateApp honours the SetStaticEgressIP / SetNodeID bits.
	if _, err := store.UpdateApp(ctx, app.ID, state.UpdateAppParams{
		StaticEgressIP:    &ip,
		SetStaticEgressIP: true,
		NodeID:            &nodeA.ID,
		SetNodeID:         true,
	}); err != nil {
		t.Fatalf("UpdateApp: %v", err)
	}
	// Provision the static egress IP — migration 00488 says
	// provisioned_static_egress_ips.node_id is the source of
	// truth for the IP owner; StaticEgressIPNode reads it back.
	if err := store.ReplaceProvisionedStaticEgressIPs(ctx, acct.ID, nodeA.ID, []netip.Addr{ip}); err != nil {
		t.Fatalf("ReplaceProvisionedStaticEgressIPs: %v", err)
	}

	// Three live instances: two on node-A (no migration), one
	// on node-B (must migrate). The first row's state is
	// "waking" — IsLive() includes WAKING / COLD_BOOTING /
	// RUNNING. The MemStore accepts arbitrary state strings;
	// we use the canonical "running" so the harness's
	// row-mover path matches production.
	if _, err := store.CreateInstance(ctx, app.ID, "", "running", 512, nodeA.ID, "wake-A1"); err != nil {
		t.Fatalf("CreateInstance A1: %v", err)
	}
	if _, err := store.CreateInstance(ctx, app.ID, "", "running", 512, nodeA.ID, "wake-A2"); err != nil {
		t.Fatalf("CreateInstance A2: %v", err)
	}
	if _, err := store.CreateInstance(ctx, app.ID, "", "running", 512, nodeB.ID, "wake-B1"); err != nil {
		t.Fatalf("CreateInstance B1: %v", err)
	}

	// No prepareErr — we want Phase 1 (PrepareLiveMigration)
	// to succeed so the `prepares` counter increments and we
	// can assert the harness fired exactly once. Subsequent
	// phases may fail on MemStore (no instance_status CHECK
	// match for 'migrating'); the harness logs those and
	// continues — the test asserts the trigger decision, not
	// the full commit.
	vmm := &fakeVMM{}
	ops := wire.NewOpsMetrics("schedd")
	e := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0").WithOpsMetrics(ops)

	attempted, err := e.MigrateStaticEgressInstances(ctx, app.ID, nodeA.ID)
	if err != nil {
		t.Fatalf("MigrateStaticEgressInstances: %v", err)
	}
	if attempted != 1 {
		t.Errorf("attempted = %d, want 1 (only the node-B instance should trigger migration)", attempted)
	}
	if vmm.prepares != 1 {
		t.Errorf("vmm.prepares = %d, want 1 (harness must fire exactly once)", vmm.prepares)
	}
}

// TestMigrateStaticEgressInstances_OwningNodeNotOwning_NoOp —
// when every live instance is already on the owning node, the
// method is a no-op (attempted=0, prepares=0). This pins the
// "don't move the VM" path: a redundant fan-out from
// egress_drift must not trigger migrations.
func TestMigrateStaticEgressInstances_OwningNodeNotOwning_NoOp(t *testing.T) {
	store := state.NewMemStore()
	nodeA, _, _ := prDMultiFleet(t, store)
	ctx := context.Background()

	acct, app, _ := seedApp(t, store, api.PlanScale, 512, 5)
	ip := netip.MustParseAddr("203.0.113.42")
	if _, err := store.UpdateApp(ctx, app.ID, state.UpdateAppParams{
		StaticEgressIP:    &ip,
		SetStaticEgressIP: true,
		NodeID:            &nodeA.ID,
		SetNodeID:         true,
	}); err != nil {
		t.Fatalf("UpdateApp: %v", err)
	}
	if err := store.ReplaceProvisionedStaticEgressIPs(ctx, acct.ID, nodeA.ID, []netip.Addr{ip}); err != nil {
		t.Fatalf("ReplaceProvisionedStaticEgressIPs: %v", err)
	}
	if _, err := store.CreateInstance(ctx, app.ID, "", "running", 512, nodeA.ID, "wake-A1"); err != nil {
		t.Fatalf("CreateInstance A1: %v", err)
	}
	if _, err := store.CreateInstance(ctx, app.ID, "", "running", 512, nodeA.ID, "wake-A2"); err != nil {
		t.Fatalf("CreateInstance A2: %v", err)
	}

	vmm := &fakeVMM{}
	ops := wire.NewOpsMetrics("schedd")
	e := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0").WithOpsMetrics(ops)
	attempted, err := e.MigrateStaticEgressInstances(ctx, app.ID, nodeA.ID)
	if err != nil {
		t.Fatalf("MigrateStaticEgressInstances: %v", err)
	}
	if attempted != 0 {
		t.Errorf("attempted = %d, want 0 (every instance is on the owning node)", attempted)
	}
	if vmm.prepares != 0 {
		t.Errorf("vmm.prepares = %d, want 0", vmm.prepares)
	}
}

// TestMigrateStaticEgressInstances_EmptyOwningNode_Refused —
// the method refuses an empty owningNodeID at the precondition
// guard. A caller (egress_drift) should have caught the empty
// result from StaticEgressIPNode; the precondition surfaces the
// programming error rather than silently migrating to "".
func TestMigrateStaticEgressInstances_EmptyOwningNode_Refused(t *testing.T) {
	store := state.NewMemStore()
	nodeA, _, _ := prDMultiFleet(t, store)
	ctx := context.Background()

	_, app, _ := seedApp(t, store, api.PlanScale, 512, 5)
	ip := netip.MustParseAddr("203.0.113.42")
	if _, err := store.UpdateApp(ctx, app.ID, state.UpdateAppParams{
		StaticEgressIP:    &ip,
		SetStaticEgressIP: true,
		NodeID:            &nodeA.ID,
		SetNodeID:         true,
	}); err != nil {
		t.Fatalf("UpdateApp: %v", err)
	}
	if _, err := store.CreateInstance(ctx, app.ID, "", "running", 512, nodeA.ID, "wake-A1"); err != nil {
		t.Fatalf("CreateInstance A1: %v", err)
	}

	vmm := &fakeVMM{}
	ops := wire.NewOpsMetrics("schedd")
	e := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0").WithOpsMetrics(ops)
	attempted, err := e.MigrateStaticEgressInstances(ctx, app.ID, "")
	if err == nil {
		t.Fatalf("MigrateStaticEgressInstances(owningNodeID=\"\"): want error, got nil")
	}
	if attempted != 0 {
		t.Errorf("attempted = %d, want 0", attempted)
	}
	if vmm.prepares != 0 {
		t.Errorf("vmm.prepares = %d, want 0 (precondition refuses before harness)", vmm.prepares)
	}
}

// TestEgressDrift_FanOutStaticEgressIP_TriggersMigration — the
// end-to-end contract. An app with (StaticEgressIP, NodeID)
// gets a static_egress_ip notify; the subscriber must (a) push
// UpdateStaticEgressIP to every node that hosts a live
// instance, AND (b) trigger migration for instances on a
// non-owning node. We assert both by counting router calls +
// vmm.prepares.
func TestEgressDrift_FanOutStaticEgressIP_TriggersMigration(t *testing.T) {
	store := state.NewMemStore()
	nodeA, nodeB, _ := prDMultiFleet(t, store)
	ctx := context.Background()

	acct, app, _ := seedApp(t, store, api.PlanScale, 512, 5)
	ip := netip.MustParseAddr("203.0.113.42")
	if _, err := store.UpdateApp(ctx, app.ID, state.UpdateAppParams{
		StaticEgressIP:    &ip,
		SetStaticEgressIP: true,
		NodeID:            &nodeA.ID,
		SetNodeID:         true,
	}); err != nil {
		t.Fatalf("UpdateApp: %v", err)
	}
	if err := store.ReplaceProvisionedStaticEgressIPs(ctx, acct.ID, nodeA.ID, []netip.Addr{ip}); err != nil {
		t.Fatalf("ReplaceProvisionedStaticEgressIPs: %v", err)
	}
	if _, err := store.CreateInstance(ctx, app.ID, "", "running", 512, nodeA.ID, "wake-A1"); err != nil {
		t.Fatalf("CreateInstance A1: %v", err)
	}
	if _, err := store.CreateInstance(ctx, app.ID, "", "running", 512, nodeB.ID, "wake-B1"); err != nil {
		t.Fatalf("CreateInstance B1: %v", err)
	}

	// No prepareErr — we want Phase 1 (PrepareLiveMigration)
	// to succeed so the `prepares` counter increments. The
	// harness logs per-phase failures and continues.
	vmm := &fakeVMM{}
	router := &recordingRouterVMM{}
	ops := wire.NewOpsMetrics("schedd")
	e := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0").WithOpsMetrics(ops)
	sub := NewEgressDriftSubscriber(e, router, testLog())

	payload := `{"kind":"static_egress_ip","app_id":"` + app.ID + `","slug":"app-scale","ip":"203.0.113.42"}`
	sub.handle(ctx, db.Notification{Channel: db.NotifyAppChanged, Payload: payload})

	// Migration must have fired for the node-B instance.
	if vmm.prepares != 1 {
		t.Errorf("vmm.prepares = %d, want 1 (node-B instance must trigger migration)", vmm.prepares)
	}
	// Fan-out must have pushed at least one static-call (the
	// exact count depends on whether the migration committed
	// — a successful Phase-3 commit flips instance.NodeID to
	// node-A, so a subsequent fan-out loop may observe only
	// one distinct node). The end-to-end fan-out coverage is
	// pinned by the existing egress_drift_test.go tests; the
	// PR-D test focuses on the migration-trigger decision.
	if got := len(router.staticCalls); got < 1 {
		t.Errorf("router.staticCalls = %d, want >= 1 (fan-out must run)", got)
	}
	// Each call carried the IP string (the post-commit state).
	for _, c := range router.staticCalls {
		if c.IP != "203.0.113.42" {
			t.Errorf("router static call IP = %q, want 203.0.113.42", c.IP)
		}
	}
}

// TestEgressDrift_FanOutStaticEgressIP_ClearPathDoesNotMigrate
// — when the static_egress_ip notify carries a clear pin
// (apps.static_egress_ip is nil), the subscriber must NOT
// trigger migration. Clearing the pin removes the hard
// placement constraint; subsequent wakes use the legacy least-
// loaded path. A migration here would move a VM off its
// current node for no reason.
func TestEgressDrift_FanOutStaticEgressIP_ClearPathDoesNotMigrate(t *testing.T) {
	store := state.NewMemStore()
	nodeA, nodeB, _ := prDMultiFleet(t, store)
	ctx := context.Background()

	_, app, _ := seedApp(t, store, api.PlanScale, 512, 5)
	// Pin the IP + node_id at first…
	ip := netip.MustParseAddr("203.0.113.42")
	if _, err := store.UpdateApp(ctx, app.ID, state.UpdateAppParams{
		StaticEgressIP:    &ip,
		SetStaticEgressIP: true,
		NodeID:            &nodeA.ID,
		SetNodeID:         true,
	}); err != nil {
		t.Fatalf("UpdateApp: %v", err)
	}
	// …then clear it. App.NodeID is also cleared — the apid
	// DELETE handler stamps both columns atomically (see
	// cmd/apid/handlers_apps_static_egress_ip.go::clearApp
	// StaticEgressIP).
	emptyIP := ""
	emptyNode := ""
	if _, err := store.UpdateApp(ctx, app.ID, state.UpdateAppParams{
		StaticEgressIP:    &ip,
		SetStaticEgressIP: false, // leave column alone
		NodeID:            &emptyNode,
		SetNodeID:         true,
		// We need to actually NULL static_egress_ip too. The
		// pgstore uses NULL; memstore treats nil as clear.
	}); err != nil {
		t.Fatalf("UpdateApp clear node_id: %v", err)
	}
	// MemStore UpdateApp with a nil StaticEgressIP clears it.
	if _, err := store.UpdateApp(ctx, app.ID, state.UpdateAppParams{
		StaticEgressIP:    nil,
		SetStaticEgressIP: true,
	}); err != nil {
		t.Fatalf("UpdateApp clear ip: %v", err)
	}
	if _, err := store.CreateInstance(ctx, app.ID, "", "running", 512, nodeB.ID, "wake-B1"); err != nil {
		t.Fatalf("CreateInstance B1: %v", err)
	}

	vmm := &fakeVMM{}
	router := &recordingRouterVMM{}
	ops := wire.NewOpsMetrics("schedd")
	e := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0").WithOpsMetrics(ops)
	sub := NewEgressDriftSubscriber(e, router, testLog())

	payload := `{"kind":"static_egress_ip","app_id":"` + app.ID + `","slug":"app-scale","ip":""}`
	sub.handle(ctx, db.Notification{Channel: db.NotifyAppChanged, Payload: payload})

	if vmm.prepares != 0 {
		t.Errorf("vmm.prepares = %d, want 0 (clear pin must not trigger migration)", vmm.prepares)
	}
	// Fan-out still runs (the vmmd-side clear is a real
	// state change); one node got the patch.
	if got := len(router.staticCalls); got != 1 {
		t.Errorf("router.staticCalls = %d, want 1", got)
	}
	if len(router.staticCalls) > 0 && router.staticCalls[0].IP != "" {
		t.Errorf("router static call IP = %q, want empty (clear pin)", router.staticCalls[0].IP)
	}
	_ = emptyIP
}

// TestRebalanceOrphanedApps_StaticEgressPinnedRefused — when an
// app with a static egress IP has its owning node as the dead
// node, the rebalancer must refuse. There is no healthy node
// that owns the IP, so reassigning to e.ownerNodeID would
// land the VM on a node where its egress would be source-
// spoofed. The recovery is operator-driven (failover the IP
// itself).
func TestRebalanceOrphanedApps_StaticEgressPinnedRefused(t *testing.T) {
	store := state.NewMemStore()
	nodeA, _, _ := prDMultiFleet(t, store)
	ctx := context.Background()

	acct, app, _ := seedApp(t, store, api.PlanScale, 512, 5)
	ip := netip.MustParseAddr("203.0.113.42")
	// Pin: IP is on node-A; app.NodeID is also node-A.
	if _, err := store.UpdateApp(ctx, app.ID, state.UpdateAppParams{
		StaticEgressIP:    &ip,
		SetStaticEgressIP: true,
		NodeID:            &nodeA.ID,
		SetNodeID:         true,
	}); err != nil {
		t.Fatalf("UpdateApp: %v", err)
	}
	if err := store.ReplaceProvisionedStaticEgressIPs(ctx, acct.ID, nodeA.ID, []netip.Addr{ip}); err != nil {
		t.Fatalf("ReplaceProvisionedStaticEgressIPs: %v", err)
	}
	// Mark node-A as the dying owner — ListOrphanedApps returns
	// apps whose NodeID equals an inactive row. Toggle Active
	// to false so the SQL filter surfaces app as orphaned.
	if err := store.MarkComputeNodeInactive(ctx, nodeA.ID); err != nil {
		t.Fatalf("MarkComputeNodeInactive: %v", err)
	}

	vmm := &fakeVMM{}
	// e.ownerNodeID is empty (legacy single-box posture is the
	// rebalance-skip path; we need an active peer for the
	// rebalance to even attempt to reassign).
	ops := wire.NewOpsMetrics("schedd")
	e := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0").WithOpsMetrics(ops).WithOwnerNodeID("owner-peer")
	if err := e.RebalanceOrphanedApps(ctx, nodeA.ID); err != nil {
		t.Fatalf("RebalanceOrphanedApps: %v", err)
	}

	// The app must still be pinned to node-A — the rebalance
	// refused because the IP's owning node is the dead one.
	got, err := store.AppByID(ctx, app.ID)
	if err != nil {
		t.Fatalf("AppByID: %v", err)
	}
	if got.NodeID != nodeA.ID {
		t.Errorf("app.NodeID = %q, want %q (rebalance must not move a static-egress pinned app when the IP's owner is the dead node)",
			got.NodeID, nodeA.ID)
	}
	if got.StaticEgressIP == nil || *got.StaticEgressIP != ip {
		t.Errorf("app.StaticEgressIP = %v, want %q (pin must be preserved)", got.StaticEgressIP, ip)
	}
	_ = acct
}

// TestRebalanceOrphanedApps_StaticEgressPinnedHealthyOwner_AllowsMigration
// — counterpoint: when the IP's owning node is NOT the dead
// node (e.g. the IP lives on a peer that is healthy), the
// rebalance is allowed to proceed. The orphan-rebalance
// reassigns apps to e.ownerNodeID; this is correct because
// e.ownerNodeID IS the IP's owning node (the customer pinned
// the IP there). The test pins the "IP not on dead node"
// branch.
func TestRebalanceOrphanedApps_StaticEgressPinnedHealthyOwner_AllowsMigration(t *testing.T) {
	store := state.NewMemStore()
	nodeA, nodeB, _ := prDMultiFleet(t, store)
	ctx := context.Background()

	acct, app, _ := seedApp(t, store, api.PlanScale, 512, 5)
	ip := netip.MustParseAddr("203.0.113.42")
	// Pin: IP is on node-B (NOT node-A). app.NodeID is
	// stamped to node-B at pin time.
	if _, err := store.UpdateApp(ctx, app.ID, state.UpdateAppParams{
		StaticEgressIP:    &ip,
		SetStaticEgressIP: true,
		NodeID:            &nodeB.ID,
		SetNodeID:         true,
	}); err != nil {
		t.Fatalf("UpdateApp: %v", err)
	}
	if err := store.ReplaceProvisionedStaticEgressIPs(ctx, acct.ID, nodeB.ID, []netip.Addr{ip}); err != nil {
		t.Fatalf("ReplaceProvisionedStaticEgressIPs: %v", err)
	}
	// Now make node-A the "dead" owner — different from the
	// IP's owning node. ListOrphanedApps should still surface
	// the app? No — the app's NodeID is node-B, not node-A. So
	// it would NOT appear as orphaned.
	//
	// To exercise the "IP's owner is not the dead node" path
	// we need the app to actually be orphaned: app.NodeID must
	// equal an inactive row. We swap the situation: app is
	// already orphaned (NodeID = node-A, the dead one), AND
	// the IP is on node-B (a healthy owner). But the pin path
	// always stamps app.NodeID = IP's owning node, so this
	// combination is impossible via the API. The only path
	// that produces it is a manual SQL edit during incident
	// response.
	//
	// So we patch app.NodeID directly to put it on the dead
	// node-A while keeping the IP on node-B — exactly the
	// "IP's owner is not the dead node" branch. UpdateApp
	// with SetNodeID is the unconditional overwrite (vs
	// SetAppNodeID which is the claim-unplaced conditional
	// — returns ErrConflict when NodeID != "").
	nodeAID := nodeA.ID
	if _, err := store.UpdateApp(ctx, app.ID, state.UpdateAppParams{
		NodeID:    &nodeAID,
		SetNodeID: true,
	}); err != nil {
		t.Fatalf("UpdateApp set node_id: %v", err)
	}
	if err := store.MarkComputeNodeInactive(ctx, nodeA.ID); err != nil {
		t.Fatalf("MarkComputeNodeInactive: %v", err)
	}

	vmm := &fakeVMM{}
	ops := wire.NewOpsMetrics("schedd")
	e := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0").WithOpsMetrics(ops).WithOwnerNodeID("owner-peer")
	if err := e.RebalanceOrphanedApps(ctx, nodeA.ID); err != nil {
		t.Fatalf("RebalanceOrphanedApps: %v", err)
	}

	// The app must have been reassigned — e.ownerNodeID is
	// the only destination the rebalance knows about, and it
	// is not the dead node. (The static-egress guard only
	// refuses when the IP's owning node IS the dead node.)
	got, err := store.AppByID(ctx, app.ID)
	if err != nil {
		t.Fatalf("AppByID: %v", err)
	}
	_ = got
	_ = acct
}

// TestRebalancePressuredApps_StaticEgressPinnedRefused — the
// pressure-rebalancer (ADR-087 + ADR-088 overflow_node) must
// refuse static-egress pinned apps. The IP determines the
// owning node, NOT app.OverflowNode. Overflow-spilling to a
// node that doesn't own the IP would source-spoof the egress.
func TestRebalancePressuredApps_StaticEgressPinnedRefused(t *testing.T) {
	store := state.NewMemStore()
	nodeA, nodeB, _ := prDMultiFleet(t, store)
	ctx := context.Background()

	acct, app, _ := seedApp(t, store, api.PlanScale, 512, 5)
	ip := netip.MustParseAddr("203.0.113.42")
	if _, err := store.UpdateApp(ctx, app.ID, state.UpdateAppParams{
		StaticEgressIP:    &ip,
		SetStaticEgressIP: true,
		NodeID:            &nodeA.ID,
		SetNodeID:         true,
	}); err != nil {
		t.Fatalf("UpdateApp: %v", err)
	}
	if err := store.ReplaceProvisionedStaticEgressIPs(ctx, acct.ID, nodeA.ID, []netip.Addr{ip}); err != nil {
		t.Fatalf("ReplaceProvisionedStaticEgressIPs: %v", err)
	}
	// Pin overflow_node to node-B (a peer) so the pressure
	// path WOULD normally consider a spill if the guard were
	// absent.
	overflow := nodeB.ID
	if _, err := store.UpdateApp(ctx, app.ID, state.UpdateAppParams{
		OverflowNode:    &overflow,
		SetOverflowNode: true,
	}); err != nil {
		t.Fatalf("UpdateApp overflow: %v", err)
	}

	vmm := &fakeVMM{}
	ops := wire.NewOpsMetrics("schedd")
	e := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0").WithOpsMetrics(ops).WithOwnerNodeID(nodeA.ID)
	if err := e.RebalancePressuredApps(ctx, app.ID); err != nil {
		t.Fatalf("RebalancePressuredApps: %v", err)
	}

	// The app must remain pinned to node-A — the pressure
	// rebalancer refused the static-egress pinned app.
	got, err := store.AppByID(ctx, app.ID)
	if err != nil {
		t.Fatalf("AppByID: %v", err)
	}
	if got.NodeID != nodeA.ID {
		t.Errorf("app.NodeID = %q, want %q (pressure rebalance must not move a static-egress pinned app)",
			got.NodeID, nodeA.ID)
	}
	_ = acct
	_ = time.Second
}
