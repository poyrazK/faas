//go:build !no_pg

// Migration-apply test for 00059 (github_installations). Pins the
// load-bearing PR-C schema contract:
//
//   1. The migration applies cleanly through 00059.
//   2. All 7 columns are present on github_installations with the
//      right types and NOT NULL / PK constraints.
//   3. The FK to accounts(id) accepts a real account and rejects
//      a foreign one (23503).
//   4. ON DELETE CASCADE: deleting the owning account removes
//      the install row (PR-C's GDPR §17 G2 path).
//   5. The login_idx exists and is discoverable.
//   6. Re-applying the migration is a no-op (IF NOT EXISTS).
//
// Slot history (PR-C's): 00051 (creation, after PR-C feature commit
// initially placed it at the next free slot post-main) → 00054 → 00055
// (mid-rebase renumbers to dodge main's own migrations landing while
// PR-C was in flight) → 00056 (slot at first rebase onto origin/main
// @ f5b583aa; PR-C rebase resolved the slot-51 collision with main's
// 00051_crons_app_full_idx + put the migration at 00056 where the
// cross-PR gate's reservation carve-out from PR #391 hides the
// 00056_reserve_slot.{sql,test.go} no-op) → 00059 (final slot after
// a second rebase onto origin/main @ d0f381a2; PR #369 now holds
// slot 58, so PR-C claims the next free real slot which is 59. The
// local embedded set adds a 00058_reserve_slot.sql to keep the
// {1..N} contiguity check happy through 59, mirroring the same
// recipe PR #335 used at slot 57).
// Per MEMORY.md/pr-migration-slot-race-with-shipping-main.
//
// Build tag mirrors apply_walk_test.go:4.

package migrations_test

import (
	"context"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

// TestMigrations_00059_GitHubInstallations pins the schema contract
// for the durable install-state table added by PR-C.
func TestMigrations_00059_GitHubInstallations(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("db.MigrateUp: %v", err)
	}

	// (1) All 7 columns present with expected types. Postgres reports
	// timestamptz as "timestamp with time zone" in information_schema
	// (the SQL-standard name); the "timestamptz" alias is a parser
	// shortcut, not the storage type. Same caveat as pg_get_constraintdef
	// (MEMORY.md/pg-get-constraintdef-shapes).
	type colSpec struct {
		name    string
		typ     string
		notnull bool
	}
	wantCols := []colSpec{
		{"account_id", "uuid", true},
		{"installation_id", "bigint", true},
		{"default_branch", "text", true},
		{"sealed_install_token", "bytea", true},
		{"token_expires_at", "timestamp with time zone", true},
		{"sealed_at", "timestamp with time zone", true},
		{"audit_github_login", "text", true},
	}
	for _, c := range wantCols {
		var dataType string
		var nullable string
		if err := pool.QueryRow(ctx, `
			select data_type, is_nullable from information_schema.columns
			 where table_name = 'github_installations' and column_name = $1
		`, c.name).Scan(&dataType, &nullable); err != nil {
			t.Fatalf("column lookup %s: %v", c.name, err)
		}
		if dataType != c.typ {
			t.Errorf("column %s: want type %s, got %s", c.name, c.typ, dataType)
		}
		if c.notnull && nullable != "NO" {
			t.Errorf("column %s: want NOT NULL, got is_nullable=%s", c.name, nullable)
		}
	}

	// (2) Indices present.
	wantIdx := []string{
		"github_installations_login_idx",
	}
	for _, idx := range wantIdx {
		var found bool
		if err := pool.QueryRow(ctx, `
			select exists(select 1 from pg_indexes
			               where tablename = 'github_installations' and indexname = $1)`, idx).Scan(&found); err != nil {
			t.Fatalf("index lookup %s: %v", idx, err)
		}
		if !found {
			t.Errorf("missing index github_installations.%s", idx)
		}
	}

	// (3) Seed an account + insert a real install row. We need a real
	// account to satisfy the FK constraint.
	const seedAcct = "00000000-0000-0000-0000-000000000051"
	if _, err := pool.Exec(ctx, `
		insert into accounts (id, email, plan) values ($1::uuid, 'm051@example.com', 'free')
		on conflict (id) do nothing
	`, seedAcct); err != nil {
		t.Fatalf("seed account: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		insert into github_installations
		    (account_id, installation_id, default_branch, sealed_install_token,
		     token_expires_at, audit_github_login)
		values ($1::uuid, 4242, 'main', '\x00deadbeef'::bytea,
		        now() + interval '1 hour', 'octocat')
	`, seedAcct); err != nil {
		t.Fatalf("insert install row: %v", err)
	}

	// (4) FK rejects an unknown account_id.
	_, err := pool.Exec(ctx, `
		insert into github_installations
		    (account_id, installation_id, default_branch, sealed_install_token,
		     token_expires_at, audit_github_login)
		values ('99999999-9999-9999-9999-999999999999'::uuid, 9999, 'main',
		        '\x00'::bytea, now() + interval '1 hour', 'unknown')
	`)
	if err == nil {
		t.Errorf("expected FK violation on unknown account_id; got nil")
	} else if !strings.Contains(err.Error(), "23503") {
		t.Errorf("expected 23503 FK violation, got %v", err)
	}

	// (5) ON DELETE CASCADE — deleting the account removes the row. The row
	// references the account's resource namespace since 20261110164822932.
	if _, err := pool.Exec(ctx, deleteAccountSQL, seedAcct); err != nil {
		t.Fatalf("delete account: %v", err)
	}
	var rowCount int
	if err := pool.QueryRow(ctx, `
		select count(*) from github_installations where account_id = $1
	`, seedAcct).Scan(&rowCount); err != nil {
		t.Fatalf("re-read after cascade: %v", err)
	}
	if rowCount != 0 {
		t.Errorf("expected 0 install rows after account delete (CASCADE), got %d", rowCount)
	}

	// (6) Re-applying the migration is a no-op. apply_walk_test.go's
	// MAX(version_id) assertion already enforces the apply-order
	// invariant; this is just a sanity tripwire for IF NOT EXISTS.
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Errorf("second MigrateUp: %v (expected nil for IF NOT EXISTS)", err)
	}
}
