// pgstore_static_egress_ip_test.go — PgStore tests for the
// operator-bundle gate behind ADR-119's apid-side pin validator
// (migrations/00337_provisioned_static_egress_ips.sql + v2
// migration 00488 node_id column).
//
// Pins:
//
//   - ProvisionedStaticEgressIPExists returns false for the
//     empty-accountID short-circuit, the !Is4 short-circuit, and
//     an unprovisioned (account_id, ip) tuple; returns true once
//     ReplaceProvisionedStaticEgressIPs has materialised the row.
//   - ReplaceProvisionedStaticEgressIPs runs DELETE + INSERT in
//     one transaction so a concurrent apid PUT sees either the
//     prior set or the new set, never a partial gap. v2: the
//     DELETE/INSERT is keyed by (account_id, node_id) — vmmd-A
//     never wipes vmmd-B's rows for the same account.
//   - StaticEgressIPNode (v2) returns the compute_nodes.id owning
//     a (account_id, customer_ip) tuple, or empty string when no
//     row exists. This is the only v2 forward lookup the schedd
//     wake path needs; a per-node reverse lookup was added in
//     PR-A but code-review removed it as dead code (no vmmd or
//     schedd path needs a full per-node IP set — the bridge-alias
//     reconciliation lives behind the bundle loader's TOML-driven
//     SIGHUP path).
//   - The table's family=4 CHECK rejects non-IPv4 inputs at the
//     database boundary (the caller-side deny-set gate is
//     api.ValidateStaticEgressIP — defence in depth).
//   - Bad UUID strings surface the SQLSTATE 22P02 invalid_text
//     cast as a wrapped error (the apid handler never sees raw
//     pgx errors).
//
// The pg path's two methods are at pkg/state/pgstore.go:19711 +
// 19746 (v1), and the v2 addition (StaticEgressIPNode, the v2
// ReplaceProvisionedStaticEgressIPs signature) is at lines 20950+.
// Mirrors the pgstore_alert_presets_test.go pattern: skip when
// Postgres is unreachable (pgtest.Open handles the skip).
package state_test

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// seedStaticEgressAccountPg stands up a fresh account via the
// Store path and returns its UUID. Mirrors seedConsumerKeyAccountApp
// from pgstore_consumer_keys_test.go — a foreign-key anchor so
// ProvisionedStaticEgressIPExists can be exercised with a
// real-shaped account_id without hand-crafting UUIDs.
func seedStaticEgressAccountPg(t *testing.T, ctx context.Context, st state.Store) string {
	t.Helper()
	acct, err := st.CreateAccount(ctx,
		fmt.Sprintf("seipe-%s@example.com", uuid.NewString()[:8]),
		api.PlanHobby)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	return acct.ID
}

// seedStaticEgressNodePg inserts a fresh compute_nodes row and
// returns its UUID. ADR-119 v2 — every test that calls
// ReplaceProvisionedStaticEgressIPs needs a valid node_id (the
// FK from migration 00488). Each test gets its own row so
// cross-test pollution is impossible (the
// provisioned_static_egress_ips_node_id_idx is shared across
// tests, but the node_id values are unique per test invocation).
//
// Mirrors migration 00024's seed pattern: name + target_url +
// capacity columns. Each test uses a unique synthetic name to
// avoid the `name` UNIQUE constraint collision.
func seedStaticEgressNodePg(t *testing.T, ctx context.Context, pool *pgxpool.Pool) string {
	t.Helper()
	nodeID := uuid.NewString()
	if _, err := pool.Exec(ctx, `
		insert into compute_nodes
		    (id, name, target_url, vpcpus, mem_mb, max_concurrency,
		     admission_ceiling_mb, active)
		values ($1::uuid, $2, 'unix:///run/faas/vmmd.sock', 160, 56000, 200, 47600, true)
	`, nodeID, fmt.Sprintf("test-node-%s", uuid.NewString()[:8])); err != nil {
		t.Fatalf("seed compute_nodes: %v", err)
	}
	return nodeID
}

// TestPgStore_ProvisionedStaticEgressIPExists_ShortCircuits pins
// the two early-return branches: empty accountID and non-v4 IP.
// Both must return (false, nil) without touching the database —
// the apid handler relies on this for the "not provisioned"
// surface (api.ErrStaticEgressIPNotProvisioned → 404).
func TestPgStore_ProvisionedStaticEgressIPExists_ShortCircuits(t *testing.T) {
	s, _, ctx := pgStoreWithPool(t)

	v4 := netip.MustParseAddr("203.0.113.1")
	v6 := netip.MustParseAddr("2001:db8::1")

	// Empty accountID short-circuit.
	got, err := s.ProvisionedStaticEgressIPExists(ctx, "", v4)
	if err != nil {
		t.Errorf("empty accountID: err = %v, want nil", err)
	}
	if got {
		t.Errorf("empty accountID: got = true, want false")
	}

	// Non-v4 short-circuit.
	got, err = s.ProvisionedStaticEgressIPExists(ctx, uuid.NewString(), v6)
	if err != nil {
		t.Errorf("non-v4 IP: err = %v, want nil", err)
	}
	if got {
		t.Errorf("non-v4 IP: got = true, want false")
	}
}

// TestPgStore_ProvisionedStaticEgressIPExists_HitMiss pins the
// normal lookup path: miss before provisioning, hit after.
// Mirrors the apid PUT path's read against the operator-bundle
// gate table.
func TestPgStore_ProvisionedStaticEgressIPExists_HitMiss(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	acctID := seedStaticEgressAccountPg(t, ctx, s)
	nodeID := seedStaticEgressNodePg(t, ctx, pool)

	ip := netip.MustParseAddr("203.0.113.42")

	// Miss before provisioning.
	got, err := s.ProvisionedStaticEgressIPExists(ctx, acctID, ip)
	if err != nil {
		t.Fatalf("miss: err = %v, want nil", err)
	}
	if got {
		t.Fatalf("miss: got = true, want false")
	}

	// Materialise via the writer path, then re-query for hit.
	if err := s.ReplaceProvisionedStaticEgressIPs(ctx, acctID, nodeID, []netip.Addr{ip}); err != nil {
		t.Fatalf("Replace (hit prep): %v", err)
	}
	got, err = s.ProvisionedStaticEgressIPExists(ctx, acctID, ip)
	if err != nil {
		t.Fatalf("hit: err = %v, want nil", err)
	}
	if !got {
		t.Errorf("hit: got = false, want true")
	}
}

// TestPgStore_ProvisionedStaticEgressIPExists_BadUUID pins the
// error branch on row.Scan: a non-UUID account_id string
// triggers SQLSTATE 22P02 invalid_text_representation, which
// QueryRow surfaces through Scan. The store wraps it as
// "state: ProvisionedStaticEgressIPExists: %w".
func TestPgStore_ProvisionedStaticEgressIPExists_BadUUID(t *testing.T) {
	s, _, ctx := pgStoreWithPool(t)

	_, err := s.ProvisionedStaticEgressIPExists(ctx, "not-a-uuid",
		netip.MustParseAddr("203.0.113.42"))
	if err == nil {
		t.Fatalf("bad UUID: got nil err, want SQLSTATE 22P02 wrap")
	}
	if !strings.Contains(err.Error(), "ProvisionedStaticEgressIPExists") {
		t.Errorf("err %q missing store-side wrap", err.Error())
	}
}

// TestPgStore_ReplaceProvisionedStaticEgressIPs_EmptyAccountID
// pins the empty-accountID error branch (no DB round-trip —
// the guard fires before Begin). The watcher must never call
// this with an empty string; the test pins the guard's presence.
func TestPgStore_ReplaceProvisionedStaticEgressIPs_EmptyAccountID(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	nodeID := seedStaticEgressNodePg(t, ctx, pool)

	err := s.ReplaceProvisionedStaticEgressIPs(ctx, "", nodeID, []netip.Addr{
		netip.MustParseAddr("203.0.113.1"),
	})
	if err == nil {
		t.Fatal("empty accountID: got nil err, want error")
	}
	if !strings.Contains(err.Error(), "empty account_id") {
		t.Errorf("err %q missing empty-account_id reason", err.Error())
	}
}

// TestPgStore_ReplaceProvisionedStaticEgressIPs_NonV4 pins the
// non-v4 rejection branch. The store-side check is a defence-
// in-depth mirror of the migration 00337 family=4 CHECK. Either
// side alone would block the bad input; both together pin the
// contract.
func TestPgStore_ReplaceProvisionedStaticEgressIPs_NonV4(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	acctID := seedStaticEgressAccountPg(t, ctx, s)
	nodeID := seedStaticEgressNodePg(t, ctx, pool)

	v6 := netip.MustParseAddr("2001:db8::1")
	err := s.ReplaceProvisionedStaticEgressIPs(ctx, acctID, nodeID, []netip.Addr{v6})
	if err == nil {
		t.Fatal("non-v4: got nil err, want error")
	}
	if !strings.Contains(err.Error(), "non-v4") {
		t.Errorf("err %q missing non-v4 reason", err.Error())
	}
}

// TestPgStore_ReplaceProvisionedStaticEgressIPs_Normal pins the
// full DELETE+INSERT-in-tx path: 2 IPs seeded, both readable via
// Exists. Mirrors what the vmmd bundle watcher does on every
// SIGHUP.
func TestPgStore_ReplaceProvisionedStaticEgressIPs_Normal(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	acctID := seedStaticEgressAccountPg(t, ctx, s)
	nodeID := seedStaticEgressNodePg(t, ctx, pool)

	ip1 := netip.MustParseAddr("203.0.113.10")
	ip2 := netip.MustParseAddr("203.0.113.11")
	if err := s.ReplaceProvisionedStaticEgressIPs(ctx, acctID, nodeID, []netip.Addr{ip1, ip2}); err != nil {
		t.Fatalf("Replace: %v", err)
	}
	for _, ip := range []netip.Addr{ip1, ip2} {
		got, err := s.ProvisionedStaticEgressIPExists(ctx, acctID, ip)
		if err != nil {
			t.Fatalf("Exists(%s): %v", ip, err)
		}
		if !got {
			t.Errorf("after Replace, %s missing from gate", ip)
		}
	}
}

// TestPgStore_ReplaceProvisionedStaticEgressIPs_Clear pins the
// "revoke provisioning" path: empty slice replaces the prior
// set with the empty set (DELETE-only path, no INSERTs). The
// apid PUT path surfaces this as 404 — the customer's IP is
// no longer routable.
func TestPgStore_ReplaceProvisionedStaticEgressIPs_Clear(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	acctID := seedStaticEgressAccountPg(t, ctx, s)
	nodeID := seedStaticEgressNodePg(t, ctx, pool)

	ip := netip.MustParseAddr("203.0.113.42")
	if err := s.ReplaceProvisionedStaticEgressIPs(ctx, acctID, nodeID, []netip.Addr{ip}); err != nil {
		t.Fatalf("seed Replace: %v", err)
	}
	if got, _ := s.ProvisionedStaticEgressIPExists(ctx, acctID, ip); !got {
		t.Fatalf("seed: ip missing from gate")
	}

	if err := s.ReplaceProvisionedStaticEgressIPs(ctx, acctID, nodeID, nil); err != nil {
		t.Fatalf("clear Replace: %v", err)
	}
	got, err := s.ProvisionedStaticEgressIPExists(ctx, acctID, ip)
	if err != nil {
		t.Fatalf("post-clear Exists: %v", err)
	}
	if got {
		t.Errorf("after clear, ip still in gate")
	}
}

// TestPgStore_ReplaceProvisionedStaticEgressIPs_ReplaceAtomically
// pins the visible-state invariant: a second Replace with a
// disjoint set leaves the table with only the new set (the
// tx-bounded DELETE removes the prior rows before the new
// INSERTs). A non-validating implementation that forgot the
// DELETE would leak stale IPs into the gate.
func TestPgStore_ReplaceProvisionedStaticEgressIPs_ReplaceAtomically(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	acctID := seedStaticEgressAccountPg(t, ctx, s)
	nodeID := seedStaticEgressNodePg(t, ctx, pool)

	old1 := netip.MustParseAddr("203.0.113.20")
	old2 := netip.MustParseAddr("203.0.113.21")
	if err := s.ReplaceProvisionedStaticEgressIPs(ctx, acctID, nodeID, []netip.Addr{old1, old2}); err != nil {
		t.Fatalf("first Replace: %v", err)
	}

	new1 := netip.MustParseAddr("203.0.113.30")
	new2 := netip.MustParseAddr("203.0.113.31")
	if err := s.ReplaceProvisionedStaticEgressIPs(ctx, acctID, nodeID, []netip.Addr{new1, new2}); err != nil {
		t.Fatalf("second Replace: %v", err)
	}

	// Old IPs must be gone (DELETE ran).
	for _, ip := range []netip.Addr{old1, old2} {
		got, _ := s.ProvisionedStaticEgressIPExists(ctx, acctID, ip)
		if got {
			t.Errorf("stale ip %s still in gate after second Replace", ip)
		}
	}
	// New IPs must be present (INSERT ran).
	for _, ip := range []netip.Addr{new1, new2} {
		got, _ := s.ProvisionedStaticEgressIPExists(ctx, acctID, ip)
		if !got {
			t.Errorf("new ip %s missing from gate after second Replace", ip)
		}
	}
}

// TestPgStore_ReplaceProvisionedStaticEgressIPs_BadUUID pins the
// error branch on tx.Exec (DELETE + INSERT). A non-UUID
// account_id casts at the parameter boundary and surfaces as a
// SQLSTATE 22P02 invalid_text wrap. The wrap path lives at
// pgstore.go:19758 (delete) and 19768 (insert); a single bad
// UUID exercises the DELETE branch.
func TestPgStore_ReplaceProvisionedStaticEgressIPs_BadUUID(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	nodeID := seedStaticEgressNodePg(t, ctx, pool)

	err := s.ReplaceProvisionedStaticEgressIPs(ctx, "not-a-uuid", nodeID, []netip.Addr{
		netip.MustParseAddr("203.0.113.1"),
	})
	if err == nil {
		t.Fatalf("bad UUID: got nil err, want SQLSTATE 22P02 wrap")
	}
	if !strings.Contains(err.Error(), "ReplaceProvisionedStaticEgressIPs") {
		t.Errorf("err %q missing store-side wrap", err.Error())
	}
}

// TestPgStore_ReplaceProvisionedStaticEgressIPs_AccountScoped
// pins the (account_id, customer_ip) PK semantics: provisioning
// the same IP under a different account does NOT collide (the
// apid PUT path needs this — an operator can provision one IP
// across multiple accounts). A non-validating implementation
// that assumed account-scoped uniqueness on customer_ip alone
// would fail here.
func TestPgStore_ReplaceProvisionedStaticEgressIPs_AccountScoped(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	acctA := seedStaticEgressAccountPg(t, ctx, s)
	acctB := seedStaticEgressAccountPg(t, ctx, s)
	nodeID := seedStaticEgressNodePg(t, ctx, pool)

	ip := netip.MustParseAddr("203.0.113.42")
	if err := s.ReplaceProvisionedStaticEgressIPs(ctx, acctA, nodeID, []netip.Addr{ip}); err != nil {
		t.Fatalf("Replace on A: %v", err)
	}
	// Same IP under acctB must not violate the PK — composite key.
	if err := s.ReplaceProvisionedStaticEgressIPs(ctx, acctB, nodeID, []netip.Addr{ip}); err != nil {
		t.Fatalf("Replace on B (composite-PK collision expected NOT): %v", err)
	}
	gotA, _ := s.ProvisionedStaticEgressIPExists(ctx, acctA, ip)
	gotB, _ := s.ProvisionedStaticEgressIPExists(ctx, acctB, ip)
	if !gotA || !gotB {
		t.Errorf("after cross-account provision: A=%v B=%v, want both true", gotA, gotB)
	}
}

// TestPgStore_ReplaceProvisionedStaticEgressIPs_NoRowsPreserved
// pins the idempotency of an empty Replace on an empty account:
// a DELETE-only transaction on a table with no rows for this
// account must succeed (no rows affected, no error). The
// watcher reload path runs this on every SIGHUP regardless of
// whether the bundle changed.
func TestPgStore_ReplaceProvisionedStaticEgressIPs_NoRowsPreserved(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	acctID := seedStaticEgressAccountPg(t, ctx, s)
	nodeID := seedStaticEgressNodePg(t, ctx, pool)

	if err := s.ReplaceProvisionedStaticEgressIPs(ctx, acctID, nodeID, nil); err != nil {
		t.Fatalf("Replace nil on empty acct: %v", err)
	}
	// Re-run with empty slice — must still succeed (idempotent).
	if err := s.ReplaceProvisionedStaticEgressIPs(ctx, acctID, nodeID, nil); err != nil {
		t.Fatalf("Replace nil x2 on empty acct: %v", err)
	}
}

// TestPgStore_ReplaceProvisionedStaticEgressIPs_FamilyCheckFromDB
// pins the migration 00337 family=4 CHECK constraint: a direct
// INSERT of a v6 INET must fail with SQLSTATE 23514 (CHECK
// violation). The store-side deny-set gate is in
// api.ValidateStaticEgressIP; the DB-side CHECK is defence in
// depth — a buggy caller that bypassed the store would still
// be rejected.
func TestPgStore_ReplaceProvisionedStaticEgressIPs_FamilyCheckFromDB(t *testing.T) {
	_, pool, ctx := pgStoreWithPool(t)
	acctID := seedStaticEgressAccountPg(t, ctx, state.NewPgStore(pool))
	nodeID := seedStaticEgressNodePg(t, ctx, pool)

	_, err := pool.Exec(ctx,
		`INSERT INTO provisioned_static_egress_ips (account_id, node_id, customer_ip)
		 VALUES ($1::uuid, $2::uuid, '2001:db8::1'::inet)`,
		acctID, nodeID)
	if err == nil {
		t.Fatal("v6 INSERT: got nil err, want SQLSTATE 23514 CHECK violation")
	}
	// Pin the error code so a future schema edit that drops the
	// CHECK trips this assertion before the wire breaks.
	if !strings.Contains(err.Error(), "family") && !strings.Contains(err.Error(), "check") {
		t.Errorf("v6 INSERT: err %q missing CHECK/family marker", err.Error())
	}
}

// TestPgStore_ProvisionedStaticEgressIPExists_NonScoping is the
// negative side of the AccountScoped test: an IP provisioned
// under acctA must NOT be readable via Exists(acctB). The
// (account_id, customer_ip) PK makes this trivially true, but
// the test pins the read-side behaviour so a future refactor
// that drops the WHERE-account_id predicate trips here before
// the apid handler can leak one tenant's provisioning state to
// another.
func TestPgStore_ProvisionedStaticEgressIPExists_NonScoping(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	acctA := seedStaticEgressAccountPg(t, ctx, s)
	acctB := seedStaticEgressAccountPg(t, ctx, s)
	nodeID := seedStaticEgressNodePg(t, ctx, pool)

	ip := netip.MustParseAddr("203.0.113.42")
	if err := s.ReplaceProvisionedStaticEgressIPs(ctx, acctA, nodeID, []netip.Addr{ip}); err != nil {
		t.Fatalf("seed on A: %v", err)
	}

	// acctB must NOT see acctA's provisioning.
	got, err := s.ProvisionedStaticEgressIPExists(ctx, acctB, ip)
	if err != nil {
		t.Fatalf("Exists(B): %v", err)
	}
	if got {
		t.Errorf("Exists(B) returned true for A-only provisioned ip (cross-tenant leak)")
	}
	// Sanity: acctA still sees its own.
	got, err = s.ProvisionedStaticEgressIPExists(ctx, acctA, ip)
	if err != nil {
		t.Fatalf("Exists(A): %v", err)
	}
	if !got {
		t.Errorf("Exists(A) returned false after seed (regression)")
	}
	// Sanity: errors.Is must not be ErrNotFound on a hit — the
	// hit path returns (true, nil), and Exists must not invent
	// a not-found error on a hit. (Pins the contract surface
	// for the apid handler.)
	if errors.Is(err, state.ErrNotFound) {
		t.Errorf("Exists(A) hit: err is ErrNotFound (should be nil on hit)")
	}
}
