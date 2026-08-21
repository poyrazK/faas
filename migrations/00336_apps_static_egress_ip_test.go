//go:build !no_pg

// Migration-apply test for 00336_apps_static_egress_ip.sql
// (ADR-119 static outbound IP per app).
//
// Pins:
//
//  1. Migration set applies cleanly through 00336 (no goose
//     duplicate-version panic). Slot 00336 was chosen via the
//     cross-PR fence precheck pattern — 00330–00333 are
//     reservation fences on this branch; 00320–00329 are claimed
//     by open PRs #1009 (public-auth-internal-only) +
//     #997 round-1 renumbering per
//     cross-pr-slot-gate-races-with-active-pr.
//  2. The two new columns exist with the expected types:
//     apps.static_egress_ip         INET NULL
//     apps.static_egress_ip_set_at  TIMESTAMPTZ NULL
//  3. The family=4 CHECK rejects IPv6 (SQLSTATE 23514).
//     Pins the v1 contract — IPv6 is deferred to follow-up.
//  4. The partial unique index `apps_static_egress_ip_key` enforces
//     one IP per app across the table. Two apps on the same
//     account pinning the same IP raises SQLSTATE 23505.
//  5. Round-trip persistence: set static_egress_ip on an app, read
//     it back, assert the family + value match.
//  6. Replay safety: re-running db.MigrateUp is a no-op. The
//     apply_walk_test harness pins this at the directory level;
//     per-migration shape is asserted here as defence in depth.
//
// Build tag matches the rest of the migration tests; set
// FAAS_SKIP_PG_TESTS=1 to skip locally (see migrations/README.md).
package migrations_test

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestMigrations_00336_AppsStaticEgressIP(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)

	// (1) Run the full migration set. 00336 should land last.
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("db.MigrateUp: %v (cross-PR fence precheck — confirm 00330–00333 are reservation fences and 00336 is the next free slot)", err)
	}

	// (2) Columns exist with expected types.
	for _, col := range []struct {
		name string
		want string
	}{
		{"static_egress_ip", "inet"},
		{"static_egress_ip_set_at", "timestamp with time zone"},
	} {
		var typ string
		err := pool.QueryRow(ctx, `
			select format_type(a.atttypid, a.atttypmod)
			  from pg_attribute a
			  join pg_class     t on t.oid = a.attrelid
			 where t.relname = 'apps'
			   and a.attname = $1`, col.name).Scan(&typ)
		if err != nil {
			t.Errorf("apps.%s: query type: %v", col.name, err)
			continue
		}
		if typ != col.want {
			t.Errorf("apps.%s type = %q, want %q", col.name, typ, col.want)
		}
	}

	// (3) The family=4 CHECK constraint exists.
	var ipCheckDef string
	err := pool.QueryRow(ctx, `
		select pg_get_constraintdef(c.oid)
		  from pg_constraint c
		  join pg_class     t on t.oid = c.conrelid
		 where t.relname = 'apps'
		   and c.conname = 'apps_static_egress_ip_family_check'`).Scan(&ipCheckDef)
	if err != nil {
		t.Fatalf("query apps_static_egress_ip_family_check: %v", err)
	}
	if ipCheckDef == "" {
		t.Fatal("apps_static_egress_ip_family_check is missing")
	}

	// (4) Partial unique index exists.
	var idxExists bool
	if err := pool.QueryRow(ctx, `
		select exists (
			select 1 from pg_indexes
			 where schemaname = current_schema()
			   and tablename  = 'apps'
			   and indexname  = 'apps_static_egress_ip_key'
		)`).Scan(&idxExists); err != nil {
		t.Fatalf("query apps_static_egress_ip_key: %v", err)
	}
	if !idxExists {
		t.Error("apps_static_egress_ip_key partial unique index is missing")
	}

	// (5) Round-trip persistence. Anchor on the first existing
	// account (the migrations/apply_walk_test harness seeds one),
	// insert a fresh app, set the IP, read back.
	var accountID string
	if err := pool.QueryRow(ctx, `select id::text from accounts limit 1`).Scan(&accountID); err != nil {
		t.Skipf("no accounts row available to anchor the test: %v", err)
	}

	var appID string
	if err := pool.QueryRow(ctx, `
		insert into apps (id, account_id, slug)
		values (gen_random_uuid(), $1::uuid, 'static-ip-test-' || gen_random_uuid()::text)
		returning id::text`, accountID).Scan(&appID); err != nil {
		t.Fatalf("insert app: %v", err)
	}

	ipStr := "203.0.113.42"
	if _, err := pool.Exec(ctx, `
		update apps
		   set static_egress_ip = $1::inet,
		       static_egress_ip_set_at = NOW()
		 WHERE id = $2::uuid`, ipStr, appID); err != nil {
		t.Fatalf("update apps.static_egress_ip: %v", err)
	}
	var gotIP *net.IP
	var gotSetAt *time.Time
	if err := pool.QueryRow(ctx, `
		select static_egress_ip, static_egress_ip_set_at
		  from apps
		 WHERE id = $1::uuid`, appID).Scan(&gotIP, &gotSetAt); err != nil {
		t.Fatalf("read apps.static_egress_ip: %v", err)
	}
	if gotIP == nil {
		t.Fatal("static_egress_ip is NULL after UPDATE")
	}
	if gotIP.String() != ipStr {
		t.Errorf("static_egress_ip round-trip = %q, want %q", gotIP.String(), ipStr)
	}
	if gotSetAt == nil {
		t.Error("static_egress_ip_set_at is NULL after UPDATE")
	}

	// (6) Family check rejects IPv6 — expect SQLSTATE 23514.
	if _, err := pool.Exec(ctx, `
		update apps
		   set static_egress_ip = '2001:db8::1'::inet
		 WHERE id = $1::uuid`, appID); err == nil {
		t.Error("expected CHECK violation when setting static_egress_ip to IPv6, got nil")
	}

	// (7) Partial unique index: insert a second app on the same
	// account, attempt to pin the same IP, expect SQLSTATE 23505.
	var dupAppID string
	if err := pool.QueryRow(ctx, `
		insert into apps (id, account_id, slug)
		values (gen_random_uuid(), $1::uuid, 'static-ip-test-dup-' || gen_random_uuid()::text)
		returning id::text`, accountID).Scan(&dupAppID); err != nil {
		t.Fatalf("insert second app for dup test: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		update apps
		   set static_egress_ip = $1::inet
		 WHERE id = $2::uuid`, ipStr, dupAppID); err == nil {
		t.Error("expected unique_violation when two apps pin the same IP, got nil")
	}

	// (8) Replay-safety: re-run MigrateUp. The harness in
	// migrations/apply_walk_test.go pins this at the directory level;
	// we re-assert here for defence in depth.
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Errorf("replay db.MigrateUp: %v (expect idempotent no-op)", err)
	}
}
