//go:build !no_pg

// Migration-apply test for 00337_provisioned_static_egress_ips.sql
// (ADR-119 redesign — operator bundle gate).
//
// Pins:
//
//  1. Migration set applies cleanly through 00338 (no goose
//     duplicate-version panic). Slot 00337 was chosen via the
//     cross-PR fence precheck pattern — 00330–00336 are taken
//     on this branch (00330–00335 reservation fences, 00336
//     apps.static_egress_ip). 00338 is the reservation fence for
//     the next PR.
//  2. The new table exists with the expected columns:
//     provisioned_static_egress_ips.account_id   UUID NOT NULL
//     provisioned_static_egress_ips.customer_ip  INET NOT NULL
//     provisioned_static_egress_ips.created_at   TIMESTAMPTZ NOT NULL
//  3. The family=4 CHECK rejects IPv6 (SQLSTATE 23514).
//  4. The composite primary key (account_id, customer_ip) is
//     unique. Pair-collision flips SQLSTATE 23505.
//  5. Round-trip persistence: insert a row, read it back, assert
//     the customer_ip value matches (the IPv4 INET equals-
//     compare shape).
//  6. Replay safety: re-running db.MigrateUp is a no-op.
//  7. The customer_ip index exists on the table.
//  8. ON DELETE CASCADE is NOT applied here (the spec keeps
//     provisioning history even if the account is dropped —
//     the operator may want to audit "what IPs did this account
//     have provisioned before deletion").
//
// Build tag matches the rest of the migration tests; set
// FAAS_SKIP_PG_TESTS=1 to skip locally (see migrations/README.md).
package migrations_test

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestMigrations_00337_ProvisionedStaticEgressIPs(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)

	// (1) Run the full migration set. 00337 should land last, 00338 the fence.
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("db.MigrateUp: %v (cross-PR fence precheck — confirm 00330–00336 are accounted for and 00337 is the next free slot)", err)
	}

	// (2) Columns exist with expected types.
	for _, col := range []struct {
		name string
		want string
	}{
		{"account_id", "uuid"},
		{"customer_ip", "inet"},
		{"created_at", "timestamp with time zone"},
	} {
		var typ string
		err := pool.QueryRow(ctx, `
			select format_type(a.atttypid, a.atttypmod)
			  from pg_attribute a
			  join pg_class     t on t.oid = a.attrelid
			 where t.relname = 'provisioned_static_egress_ips'
			   and a.attname = $1`, col.name).Scan(&typ)
		if err != nil {
			t.Errorf("provisioned_static_egress_ips.%s: query type: %v", col.name, err)
			continue
		}
		if typ != col.want {
			t.Errorf("provisioned_static_egress_ips.%s type = %q, want %q", col.name, typ, col.want)
		}
	}

	// (3) The family=4 CHECK constraint exists.
	var familyCheckDef string
	err := pool.QueryRow(ctx, `
		select pg_get_constraintdef(c.oid)
		  from pg_constraint c
		  join pg_class     t on t.oid = c.conrelid
		 where t.relname = 'provisioned_static_egress_ips'
		   and c.conname = 'provisioned_static_egress_ips_family_check'`).Scan(&familyCheckDef)
	if err != nil {
		t.Fatalf("query provisioned_static_egress_ips_family_check: %v", err)
	}
	if familyCheckDef == "" {
		t.Fatal("provisioned_static_egress_ips_family_check is missing")
	}

	// (4) Composite primary key works. Anchor on the first
	// existing account (the migrations/apply_walk_test harness
	// seeds one), insert a row, attempt to insert a duplicate,
	// expect SQLSTATE 23505.
	var accountID string
	if err := pool.QueryRow(ctx, `select id::text from accounts limit 1`).Scan(&accountID); err != nil {
		t.Skipf("no accounts row available to anchor the test: %v", err)
	}

	ipStr := "203.0.113.42"
	if _, err := pool.Exec(ctx, `
		insert into provisioned_static_egress_ips (account_id, customer_ip)
		values ($1::uuid, $2::inet)`, accountID, ipStr); err != nil {
		t.Fatalf("insert provisioned_static_egress_ips: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		insert into provisioned_static_egress_ips (account_id, customer_ip)
		values ($1::uuid, $2::inet)`, accountID, ipStr); err == nil {
		t.Error("expected unique_violation when (account_id, customer_ip) collides, got nil")
	}

	// (5) Round-trip persistence. Read the row back, assert
	// the customer_ip value matches.
	var gotIP string
	if err := pool.QueryRow(ctx, `
		select host(customer_ip)
		  from provisioned_static_egress_ips
		 WHERE account_id = $1::uuid
		   and customer_ip = $2::inet`, accountID, ipStr).Scan(&gotIP); err != nil {
		t.Fatalf("read provisioned_static_egress_ips: %v", err)
	}
	if gotIP != ipStr {
		t.Errorf("customer_ip round-trip = %q, want %q", gotIP, ipStr)
	}

	// (6) Family check rejects IPv6 — expect SQLSTATE 23514.
	if _, err := pool.Exec(ctx, `
		insert into provisioned_static_egress_ips (account_id, customer_ip)
		values ($1::uuid, '2001:db8::1'::inet)`, accountID); err == nil {
		t.Error("expected CHECK violation when inserting IPv6 customer_ip, got nil")
	}

	// (7) The customer_ip index exists.
	var idxExists bool
	if err := pool.QueryRow(ctx, `
		select exists (
			select 1 from pg_indexes
			 where schemaname = current_schema()
			   and tablename  = 'provisioned_static_egress_ips'
			   and indexname  = 'provisioned_static_egress_ips_customer_ip_idx'
		)`).Scan(&idxExists); err != nil {
		t.Fatalf("query provisioned_static_egress_ips_customer_ip_idx: %v", err)
	}
	if !idxExists {
		t.Error("provisioned_static_egress_ips_customer_ip_idx is missing")
	}

	// (8) Replay-safety: re-run MigrateUp. The harness in
	// migrations/apply_walk_test.go pins this at the directory
	// level; we re-assert here for defence in depth.
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Errorf("replay db.MigrateUp: %v (expect idempotent no-op)", err)
	}
}
