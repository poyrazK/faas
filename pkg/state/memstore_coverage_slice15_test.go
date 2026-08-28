package state

import (
	"errors"
	"net/netip"
	"strings"
	"testing"
)

// TestCoverageSlice15StaticEgressIPSet drives the new
// SetStaticEgressIP branch in memstore.go::UpdateApp (ADR-119). The
// Set-bit convention is identical to SetOverflowNode / SetCORS*:
// SetStaticEgressIP=true with a non-nil pointer writes the IP and
// stamps SetAt; SetStaticEgressIP=true with nil clears both.
func TestCoverageSlice15StaticEgressIPSet(t *testing.T) {
	m, ctx, _, app, _ := memCoverageFixture(t)

	ip := netip.MustParseAddr("203.0.113.42")
	updated, err := m.UpdateApp(ctx, app.ID, UpdateAppParams{
		SetStaticEgressIP: true,
		StaticEgressIP:    &ip,
	})
	if err != nil {
		t.Fatalf("UpdateApp set static: %v", err)
	}
	if updated.StaticEgressIP == nil {
		t.Fatal("StaticEgressIP nil after set")
	}
	if updated.StaticEgressIP.String() != ip.String() {
		t.Errorf("StaticEgressIP = %s, want %s", updated.StaticEgressIP, ip)
	}
	if updated.StaticEgressIPSetAt == nil {
		t.Error("StaticEgressIPSetAt nil after set")
	}
}

// TestCoverageSlice15StaticEgressIPClear drives the nil-pointer
// clears-both-columns branch. Used by the DELETE wire shape.
func TestCoverageSlice15StaticEgressIPClear(t *testing.T) {
	m, ctx, _, app, _ := memCoverageFixture(t)

	ip := netip.MustParseAddr("203.0.113.42")
	if _, err := m.UpdateApp(ctx, app.ID, UpdateAppParams{
		SetStaticEgressIP: true,
		StaticEgressIP:    &ip,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	updated, err := m.UpdateApp(ctx, app.ID, UpdateAppParams{
		SetStaticEgressIP: true,
		StaticEgressIP:    nil,
	})
	if err != nil {
		t.Fatalf("UpdateApp clear static: %v", err)
	}
	if updated.StaticEgressIP != nil {
		t.Errorf("StaticEgressIP = %s after clear, want nil", updated.StaticEgressIP)
	}
	if updated.StaticEgressIPSetAt != nil {
		t.Errorf("StaticEgressIPSetAt = %s after clear, want nil", updated.StaticEgressIPSetAt)
	}
}

// TestCoverageSlice15StaticEgressIPNoTouch drives the
// SetStaticEgressIP=false (don't touch) branch. The columns must
// remain at their pre-PATCH values.
func TestCoverageSlice15StaticEgressIPNoTouch(t *testing.T) {
	m, ctx, _, app, _ := memCoverageFixture(t)

	ip := netip.MustParseAddr("203.0.113.42")
	if _, err := m.UpdateApp(ctx, app.ID, UpdateAppParams{
		SetStaticEgressIP: true,
		StaticEgressIP:    &ip,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	before, err := m.AppByID(ctx, app.ID)
	if err != nil {
		t.Fatalf("AppByID: %v", err)
	}

	// PATCH with SetStaticEgressIP=false (the default-zero path).
	other := netip.MustParseAddr("198.51.100.7")
	if _, err := m.UpdateApp(ctx, app.ID, UpdateAppParams{
		StaticEgressIP: &other, // ignored when Set is false
	}); err != nil {
		t.Fatalf("UpdateApp no-touch: %v", err)
	}
	after, err := m.AppByID(ctx, app.ID)
	if err != nil {
		t.Fatalf("AppByID: %v", err)
	}
	if after.StaticEgressIP == nil || after.StaticEgressIP.String() != before.StaticEgressIP.String() {
		t.Errorf("StaticEgressIP changed during no-touch PATCH: %s vs %s", after.StaticEgressIP, before.StaticEgressIP)
	}
}

// TestCoverageSlice15StaticEgressIPDefaultIsZero pins the fixture
// invariant: a fresh app has StaticEgressIP=nil + SetAt=nil. The
// migration 00325 default is NULL on both columns, and the
// MemStore's CreateApp mirrors that.
func TestCoverageSlice15StaticEgressIPDefaultIsZero(t *testing.T) {
	m, ctx, _, app, _ := memCoverageFixture(t)
	got, err := m.AppByID(ctx, app.ID)
	if err != nil {
		t.Fatalf("AppByID: %v", err)
	}
	if got.StaticEgressIP != nil {
		t.Errorf("fresh StaticEgressIP = %s, want nil", got.StaticEgressIP)
	}
	if got.StaticEgressIPSetAt != nil {
		t.Errorf("fresh StaticEgressIPSetAt = %s, want nil", got.StaticEgressIPSetAt)
	}
}

// TestCoverageSlice15StaticEgressIPCrossAppConflict drives the
// same-account cross-app conflict branch in memstore.go::UpdateApp.
// Mirrors the pgstore's apps_static_egress_ip_key partial unique
// index — the apid handler branches on errors.Is(err, ErrConflict)
// and the index-name substring to return 403 plan_static_egress_ip_quota.
func TestCoverageSlice15StaticEgressIPCrossAppConflict(t *testing.T) {
	m, ctx, _, app, _ := memCoverageFixture(t)

	ip := netip.MustParseAddr("203.0.113.42")
	if _, err := m.UpdateApp(ctx, app.ID, UpdateAppParams{
		SetStaticEgressIP: true,
		StaticEgressIP:    &ip,
	}); err != nil {
		t.Fatalf("first UpdateApp: %v", err)
	}

	// Second app on the same account tries to pin the same IP.
	second, err := m.CreateApp(ctx, App{
		AccountID: app.AccountID,
		Slug:      "second-app-" + app.Slug,
		RAMMB:     256,
		Status:    AppActive,
	})
	if err != nil {
		t.Fatalf("CreateApp second: %v", err)
	}
	_, err = m.UpdateApp(ctx, second.ID, UpdateAppParams{
		SetStaticEgressIP: true,
		StaticEgressIP:    &ip,
	})
	if err == nil {
		t.Fatal("expected ErrConflict on cross-app same-IP, got nil")
	}
	if !errors.Is(err, ErrConflict) {
		t.Errorf("err = %v, want ErrConflict", err)
	}
	if !strings.Contains(err.Error(), "apps_static_egress_ip_key") {
		t.Errorf("err %q missing index name (apId handler branch)", err.Error())
	}
}

// TestCoverageSlice15StaticEgressIPCrossAppLoopAllBranches drives
// the loop body in memstore.go::UpdateApp's cross-app unique-IP
// guard. The guard walks every other app and skips 3 cases before
// raising ErrConflict:
//  1. self (otherID == id)
//  2. cross-account (other.AccountID != a.AccountID)
//  3. ip not set (other.StaticEgressIP == nil)
//
// We seed 4 apps to exercise all 3 skip branches + the match branch:
//   - self: the target app itself
//   - other-account: a second account's app with the conflicting IP set
//   - same-account no-ip: a sibling on the same account with no IP
//   - same-account matching-ip: a sibling on the same account with the same IP
func TestCoverageSlice15StaticEgressIPCrossAppLoopAllBranches(t *testing.T) {
	m, ctx, _, app, _ := memCoverageFixture(t)

	ip := netip.MustParseAddr("203.0.113.42")

	// 1) other-account sibling — must be skipped by the loop.
	_, _, _, otherAcct, _ := memCoverageFixtureOrg(t)
	otherAcctApp, err := m.CreateApp(ctx, App{
		AccountID: otherAcct.ID,
		Slug:      "other-acct-app",
		RAMMB:     256,
		Status:    AppActive,
	})
	if err != nil {
		t.Fatalf("CreateApp other: %v", err)
	}
	// Pre-pin the conflicting IP on the OTHER account so the loop
	// has a candidate that should NOT raise conflict (skip branch).
	if _, err := m.UpdateApp(ctx, otherAcctApp.ID, UpdateAppParams{
		SetStaticEgressIP: true,
		StaticEgressIP:    &ip,
	}); err != nil {
		t.Fatalf("seed other-acct pin: %v", err)
	}

	// 2) same-account sibling with no IP — must be skipped.
	noIPApp, err := m.CreateApp(ctx, App{
		AccountID: app.AccountID,
		Slug:      "same-acct-no-ip-" + app.Slug,
		RAMMB:     256,
		Status:    AppActive,
	})
	if err != nil {
		t.Fatalf("CreateApp same-acct no-ip: %v", err)
	}

	// 3) Pin the target IP on `app` first.
	if _, err := m.UpdateApp(ctx, app.ID, UpdateAppParams{
		SetStaticEgressIP: true,
		StaticEgressIP:    &ip,
	}); err != nil {
		t.Fatalf("seed app pin: %v", err)
	}

	// 4) Same-account sibling with a DIFFERENT IP — must not conflict.
	differentIP := netip.MustParseAddr("198.51.100.7")
	if _, err := m.UpdateApp(ctx, noIPApp.ID, UpdateAppParams{
		SetStaticEgressIP: true,
		StaticEgressIP:    &differentIP,
	}); err != nil {
		t.Fatalf("set noIPApp different IP (no conflict expected): %v", err)
	}

	// 5) Try to pin `ip` on a NEW same-account app — must conflict.
	freshSameAcct, err := m.CreateApp(ctx, App{
		AccountID: app.AccountID,
		Slug:      "fresh-same-acct-" + app.Slug,
		RAMMB:     256,
		Status:    AppActive,
	})
	if err != nil {
		t.Fatalf("CreateApp fresh: %v", err)
	}
	_, err = m.UpdateApp(ctx, freshSameAcct.ID, UpdateAppParams{
		SetStaticEgressIP: true,
		StaticEgressIP:    &ip,
	})
	if err == nil {
		t.Fatal("expected ErrConflict on cross-app same-IP, got nil")
	}
	if !errors.Is(err, ErrConflict) {
		t.Errorf("err = %v, want ErrConflict", err)
	}
}

// TestCoverageSlice15ProvisionedStaticEgressIPExists pins the
// provisioned-bucket lookup (ADR-119 operator-bundle gate).
// Covers the empty-accountID short-circuit + the !Is4 short-circuit
// + the normal hit + the normal miss branches.
func TestCoverageSlice15ProvisionedStaticEgressIPExists(t *testing.T) {
	m, ctx, _, app, _ := memCoverageFixture(t)

	ip := netip.MustParseAddr("203.0.113.42")

	// Empty accountID short-circuit returns (false, nil).
	got, err := m.ProvisionedStaticEgressIPExists(ctx, "", ip)
	if err != nil || got {
		t.Errorf("empty accountID: got (%v, %v), want (false, nil)", got, err)
	}

	// Non-v4 IP short-circuit returns (false, nil).
	v6 := netip.MustParseAddr("2001:db8::1")
	got, err = m.ProvisionedStaticEgressIPExists(ctx, app.AccountID, v6)
	if err != nil || got {
		t.Errorf("non-v4 IP: got (%v, %v), want (false, nil)", got, err)
	}

	// Miss: nothing seeded for this account yet.
	got, err = m.ProvisionedStaticEgressIPExists(ctx, app.AccountID, ip)
	if err != nil || got {
		t.Errorf("miss: got (%v, %v), want (false, nil)", got, err)
	}

	// Seed and re-query for hit.
	if err := m.ReplaceProvisionedStaticEgressIPs(ctx, app.AccountID, "test-node", []netip.Addr{ip}); err != nil {
		t.Fatalf("seed Replace: %v", err)
	}
	got, err = m.ProvisionedStaticEgressIPExists(ctx, app.AccountID, ip)
	if err != nil || !got {
		t.Errorf("hit: got (%v, %v), want (true, nil)", got, err)
	}
}

// TestCoverageSlice15ReplaceProvisionedStaticEgressIPs pins the
// provisioned-bucket write (ADR-119 vmmd SIGHUP path).
// Covers the empty-accountID error branch + the non-v4 IP error
// branch + the normal replace (clear-then-insert) branch.
// v2 adds the nodeID parameter; the test uses a synthetic
// test-node id (the MemStore has no FK constraint).
func TestCoverageSlice15ReplaceProvisionedStaticEgressIPs(t *testing.T) {
	m, ctx, _, app, _ := memCoverageFixture(t)

	// Empty accountID returns error.
	if err := m.ReplaceProvisionedStaticEgressIPs(ctx, "", "test-node", nil); err == nil {
		t.Error("empty accountID: got nil err, want error")
	}

	// Non-v4 IP in the slice returns error.
	v6 := netip.MustParseAddr("2001:db8::1")
	if err := m.ReplaceProvisionedStaticEgressIPs(ctx, app.AccountID, "test-node", []netip.Addr{v6}); err == nil {
		t.Error("non-v4 IP: got nil err, want error")
	}

	// Normal replace: 2 IPs, then a clear (empty slice replaces with empty bucket).
	ip1 := netip.MustParseAddr("203.0.113.1")
	ip2 := netip.MustParseAddr("203.0.113.2")
	if err := m.ReplaceProvisionedStaticEgressIPs(ctx, app.AccountID, "test-node", []netip.Addr{ip1, ip2}); err != nil {
		t.Fatalf("first replace: %v", err)
	}
	for _, ip := range []netip.Addr{ip1, ip2} {
		got, _ := m.ProvisionedStaticEgressIPExists(ctx, app.AccountID, ip)
		if !got {
			t.Errorf("after first replace, %s missing from bucket", ip)
		}
	}

	// Clear: empty slice replaces with empty bucket.
	if err := m.ReplaceProvisionedStaticEgressIPs(ctx, app.AccountID, "test-node", nil); err != nil {
		t.Fatalf("clear replace: %v", err)
	}
	for _, ip := range []netip.Addr{ip1, ip2} {
		got, _ := m.ProvisionedStaticEgressIPExists(ctx, app.AccountID, ip)
		if got {
			t.Errorf("after clear, %s still in bucket", ip)
		}
	}
}

// TestCoverageSlice15StaticEgressIPNode_V2 (ADR-119 v2 round-2)
// pins the MemStore implementation of StaticEgressIPNode — the
// schedd-side lookup that returns the compute_nodes.id owning a
// (account_id, customer_ip) tuple. v2 partition: per-(account, node).
//
// Covers:
//   - empty accountID short-circuit → ("", nil)
//   - non-v4 IP short-circuit → ("", nil)
//   - miss before provisioning → ("", nil)
//   - hit returns the nodeID passed to ReplaceProvisionedStaticEgressIPs
//   - cross-account miss for the same IP is independent
func TestCoverageSlice15StaticEgressIPNode_V2(t *testing.T) {
	m, ctx, _, app, _ := memCoverageFixture(t)
	_, _, _, otherAcct, _ := memCoverageFixtureOrg(t)

	ip := netip.MustParseAddr("203.0.113.42")

	// Empty accountID short-circuit.
	if got, err := m.StaticEgressIPNode(ctx, "", ip); err != nil || got != "" {
		t.Errorf("empty accountID: got (%q, %v), want (\"\", nil)", got, err)
	}

	// Non-v4 IP short-circuit.
	v6 := netip.MustParseAddr("2001:db8::1")
	if got, err := m.StaticEgressIPNode(ctx, app.AccountID, v6); err != nil || got != "" {
		t.Errorf("non-v4 IP: got (%q, %v), want (\"\", nil)", got, err)
	}

	// Miss before provisioning.
	if got, err := m.StaticEgressIPNode(ctx, app.AccountID, ip); err != nil || got != "" {
		t.Errorf("miss: got (%q, %v), want (\"\", nil)", got, err)
	}

	// Provision on a specific node.
	if err := m.ReplaceProvisionedStaticEgressIPs(ctx, app.AccountID, "node-A", []netip.Addr{ip}); err != nil {
		t.Fatalf("Replace: %v", err)
	}
	if got, err := m.StaticEgressIPNode(ctx, app.AccountID, ip); err != nil || got != "node-A" {
		t.Errorf("hit: got (%q, %v), want (node-A, nil)", got, err)
	}

	// Cross-account miss — otherAcct has no rows for ip.
	if got, err := m.StaticEgressIPNode(ctx, otherAcct.ID, ip); err != nil || got != "" {
		t.Errorf("cross-account miss: got (%q, %v), want (\"\", nil)", got, err)
	}
}

// TestCoverageSlice15ProvisionedStaticEgressIPsForNode_V2 (ADR-119 v2
// round-2) pins the per-node reverse lookup used by vmmd's bundle
// loader on SIGHUP to reconcile its bridge alias-IP set.
//
// Covers:
//   - empty nodeID short-circuit → (nil, nil) (the loader's
//     reconcile is a no-op when the vmmd has no own nodeID —
//     single-box legacy fallback in cmd/vmmd/main.go's
//     egressBundleNodeID helper).
//   - miss returns nil (not error) when the node has no IPs.
//   - hit returns the per-node set across all accounts.
//   - per-node partition: node-A's IPs don't bleed into node-B's
//     result.
func TestCoverageSlice15ProvisionedStaticEgressIPsForNode_V2(t *testing.T) {
	m, ctx, _, app, _ := memCoverageFixture(t)
	_, _, _, otherAcct, _ := memCoverageFixtureOrg(t)

	ipA1 := netip.MustParseAddr("203.0.113.1")
	ipA2 := netip.MustParseAddr("203.0.113.2")
	ipB1 := netip.MustParseAddr("198.51.100.7")

	// Empty nodeID short-circuit.
	if got, err := m.ProvisionedStaticEgressIPsForNode(ctx, ""); err != nil || got != nil {
		t.Errorf("empty nodeID: got (%v, %v), want (nil, nil)", got, err)
	}

	// Miss on an unprovisioned node.
	if got, err := m.ProvisionedStaticEgressIPsForNode(ctx, "node-X"); err != nil || got != nil {
		t.Errorf("miss: got (%v, %v), want (nil, nil)", got, err)
	}

	// node-A: 2 IPs across two accounts.
	if err := m.ReplaceProvisionedStaticEgressIPs(ctx, app.AccountID, "node-A", []netip.Addr{ipA1}); err != nil {
		t.Fatalf("seed node-A app: %v", err)
	}
	if err := m.ReplaceProvisionedStaticEgressIPs(ctx, otherAcct.ID, "node-A", []netip.Addr{ipA2}); err != nil {
		t.Fatalf("seed node-A other: %v", err)
	}
	// node-B: 1 IP for app.
	if err := m.ReplaceProvisionedStaticEgressIPs(ctx, app.AccountID, "node-B", []netip.Addr{ipB1}); err != nil {
		t.Fatalf("seed node-B: %v", err)
	}

	gotA, err := m.ProvisionedStaticEgressIPsForNode(ctx, "node-A")
	if err != nil {
		t.Fatalf("reverse node-A: %v", err)
	}
	if len(gotA) != 2 {
		t.Errorf("node-A: got %d IPs (%v), want 2", len(gotA), gotA)
	}

	gotB, err := m.ProvisionedStaticEgressIPsForNode(ctx, "node-B")
	if err != nil {
		t.Fatalf("reverse node-B: %v", err)
	}
	if len(gotB) != 1 || gotB[0] != ipB1 {
		t.Errorf("node-B: got %v, want [%s]", gotB, ipB1)
	}
}
