//go:build !no_pg

// pgstore_static_egress_ip_v2_test.go — ADR-119 v2 round-2
// tests for the per-node static egress IP surface
// (migrations/00488_provisioned_static_egress_ips_node_id.sql).
//
// Pins the new v2 methods on PgStore:
//   - StaticEgressIPNode: returns the compute_nodes.id owning
//     a (account_id, customer_ip) tuple. This is the only
//     forward lookup the schedd wake path needs (a reverse
//     per-node list was rejected at code-review time as dead
//     code — nothing in vmmd or schedd calls for a full
//     per-node IP set; the bridge-alias reconciliation lives
//     behind the bundle loader's TOML-driven SIGHUP path).
//   - ReplaceProvisionedStaticEgressIPs's v2 per-(account, node)
//     partitioning: vmmd-A's writes never collide with vmmd-B's.
//
// These tests are the round-2 follow-up to the v1 round-7
// unblocker (pgstore_static_egress_ip_test.go). The v1 file is
// untouched — its 11 tests cover the v1 surface (account-scoped
// gate). The v2 file extends the coverage to the per-node
// dimension without rewriting the v1 cases.

package state_test

import (
	"context"
	"net/netip"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/state"
)

// seedStaticEgressNodePgWithID is a variant of seedStaticEgressNodePg
// that uses a caller-provided nodeID (rather than a fresh uuid.NewString()).
// Useful when the test wants to assert a specific node_id value
// comes back through StaticEgressIPNode.
func seedStaticEgressNodePgWithID(t *testing.T, ctx context.Context, pool *pgxpool.Pool, nodeID string) {
	t.Helper()
	if _, err := pool.Exec(ctx, `
		insert into compute_nodes
		    (id, name, target_url, vpcpus, mem_mb, max_concurrency,
		     admission_ceiling_mb, active)
		values ($1::uuid, $2, 'unix:///run/faas/vmmd.sock', 160, 56000, 200, 47600, true)
	`, nodeID, "test-node-"+uuid.NewString()[:8]); err != nil {
		t.Fatalf("seed compute_nodes: %v", err)
	}
}

// TestPgStore_StaticEgressIPNode_HitMiss pins the v2 lookup
// path. The schedd wake path uses this method to stamp
// Request.RequiredNodeID before placement (defence-in-depth
// alongside the apid PUT path's ProvisionedStaticEgressIPExists).
//
// Pins:
//   - empty accountID short-circuits to (empty, nil) — matches
//     ProvisionedStaticEgressIPExists's surface.
//   - non-v4 IP short-circuits to (empty, nil).
//   - miss returns (empty, nil) — no error, just empty.
//   - hit returns the seeded node_id.
func TestPgStore_StaticEgressIPNode_HitMiss(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	acctID := seedStaticEgressAccountPg(t, ctx, s)
	nodeID := seedStaticEgressNodePg(t, ctx, pool)

	ip := netip.MustParseAddr("203.0.113.42")

	// Short-circuits.
	if got, err := s.StaticEgressIPNode(ctx, "", ip); err != nil || got != "" {
		t.Errorf("empty accountID: got (%q, %v), want (\"\", nil)", got, err)
	}
	v6 := netip.MustParseAddr("2001:db8::1")
	if got, err := s.StaticEgressIPNode(ctx, acctID, v6); err != nil || got != "" {
		t.Errorf("non-v4 IP: got (%q, %v), want (\"\", nil)", got, err)
	}

	// Miss before provisioning.
	if got, err := s.StaticEgressIPNode(ctx, acctID, ip); err != nil || got != "" {
		t.Errorf("miss: got (%q, %v), want (\"\", nil)", got, err)
	}

	// Hit after Replace.
	if err := s.ReplaceProvisionedStaticEgressIPs(ctx, acctID, nodeID, []netip.Addr{ip}); err != nil {
		t.Fatalf("Replace: %v", err)
	}
	got, err := s.StaticEgressIPNode(ctx, acctID, ip)
	if err != nil {
		t.Fatalf("hit: err = %v, want nil", err)
	}
	if got != nodeID {
		t.Errorf("hit: got %q, want %q", got, nodeID)
	}
}

// TestPgStore_StaticEgressIPNode_BadUUID pins the error branch
// on row.Scan: a non-UUID account_id string triggers SQLSTATE
// 22P02 invalid_text_representation. Mirrors
// ProvisionedStaticEgressIPExists_BadUUID — same wrap path.
func TestPgStore_StaticEgressIPNode_BadUUID(t *testing.T) {
	s, _, ctx := pgStoreWithPool(t)

	_, err := s.StaticEgressIPNode(ctx, "not-a-uuid",
		netip.MustParseAddr("203.0.113.42"))
	if err == nil {
		t.Fatalf("bad UUID: got nil err, want SQLSTATE 22P02 wrap")
	}
}

// TestPgStore_ReplaceProvisionedStaticEgressIPs_NodeIDFilter
// pins the v2 per-(account, node) partition: vmmd-A's writes
// never collide with vmmd-B's writes for the same account. A
// non-validating implementation that keyed the DELETE by
// account_id alone would silently wipe vmmd-B's rows.
//
// Pins:
//   - nodeA's Replace leaves nodeB's rows intact for the same
//     account.
//   - nodeB's Replace leaves nodeA's rows intact for the same
//     account.
//   - both nodes' rows coexist and are independently readable
//     via StaticEgressIPNode (the per-IP forward lookup covers
//     the per-node partition: each surviving IP's owning node
//     matches what its writer stamped).
func TestPgStore_ReplaceProvisionedStaticEgressIPs_NodeIDFilter(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	acctID := seedStaticEgressAccountPg(t, ctx, s)
	nodeA := seedStaticEgressNodePg(t, ctx, pool)
	nodeB := seedStaticEgressNodePg(t, ctx, pool)

	ipA := netip.MustParseAddr("203.0.113.10")
	ipB := netip.MustParseAddr("198.51.100.20")

	// vmmd-A writes ipA for acctID.
	if err := s.ReplaceProvisionedStaticEgressIPs(ctx, acctID, nodeA, []netip.Addr{ipA}); err != nil {
		t.Fatalf("seed nodeA: %v", err)
	}
	// vmmd-B writes ipB for acctID (same account, different node).
	if err := s.ReplaceProvisionedStaticEgressIPs(ctx, acctID, nodeB, []netip.Addr{ipB}); err != nil {
		t.Fatalf("seed nodeB: %v", err)
	}

	// Both IPs visible via the account-scoped Exists.
	if got, _ := s.ProvisionedStaticEgressIPExists(ctx, acctID, ipA); !got {
		t.Errorf("after seeding both nodes: ipA missing from Exists")
	}
	if got, _ := s.ProvisionedStaticEgressIPExists(ctx, acctID, ipB); !got {
		t.Errorf("after seeding both nodes: ipB missing from Exists")
	}

	// vmmd-A re-Replace with empty slice (revoke provisioning on nodeA).
	// MUST NOT touch nodeB's row.
	if err := s.ReplaceProvisionedStaticEgressIPs(ctx, acctID, nodeA, nil); err != nil {
		t.Fatalf("clear nodeA: %v", err)
	}
	if got, _ := s.ProvisionedStaticEgressIPExists(ctx, acctID, ipA); got {
		t.Errorf("after clear nodeA: ipA still in gate (DELETE leaked across nodes)")
	}
	if got, _ := s.ProvisionedStaticEgressIPExists(ctx, acctID, ipB); !got {
		t.Errorf("after clear nodeA: ipB missing from gate (DELETE leaked across nodes)")
	}

	// StaticEgressIPNode confirms the per-node partition: each
	// IP's owning node is exactly what the writer stamped, and
	// clearing nodeA did not orphan ipB's row.
	if got, _ := s.StaticEgressIPNode(ctx, acctID, ipA); got != "" {
		t.Errorf("ipA: got %q, want \"\" (cleared from nodeA)", got)
	}
	if got, _ := s.StaticEgressIPNode(ctx, acctID, ipB); got != nodeB {
		t.Errorf("ipB: got %q, want %q (preserved on nodeB)", got, nodeB)
	}
}

// _ = state.DefaultLocalNodeName — suppress unused import when
// the file is built without the !no_pg tag (the import is needed
// for the seed helpers above; this is a no-op safety net).
var _ = state.DefaultLocalNodeName
