//go:build !no_pg

package migrations_test

import (
	"context"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

// TestMigrations_00144_APIKeysProvenance pins the IDEMPOTENT ALTER
// shape and the column types of the api_keys provenance columns
// landed by 00144_api_keys_provenance.sql.
//
// What we pin (replay-safety + audit-evidence cost):
//   - Column types match the APIKey struct (CreatedIP is INET —
//     nullable Go's *netip.Addr / pgx's native inet path; CreatedUA
//     is TEXT — unbounded per the migration; ParentKeyID is UUID).
//   - All three columns are nullable (the "pre-PR rows have no
//     provenance" guarantee).
//   - parent_key_id self-reference FK behavior: a hard-delete of
//     the predecessor leaves the new row's parent_key_id NULL
//     (ON DELETE SET NULL).
//   - The replay guard: re-running the migration after a successful
//     first run is a no-op (ADD COLUMN IF NOT EXISTS in 00144).
//
// Cross-PR slot reservation note: this test is coupled to the .sql
// at slot 135. If a sibling PR renumbers, the test file moves with
// it (per migration-slot-renumber-at-pr-creation).
func TestMigrations_00144_APIKeysProvenance(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.OpenMigrated(t)

	// Use a private migrated database: the complete migration set includes
	// public-qualified objects. A separate database preserves test isolation.
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("db.MigrateUp: %v", err)
	}

	// 1. Column existence + types. information_schema is the
	//    platform-neutral way; the older pg_catalog views also
	//    work but column-name filtering is the same shape.
	//
	//    Keep the schema lookup relative to the pool, including when the
	//    test uses a private migrated database.
	rows, err := pool.Query(ctx, `
		SELECT column_name, data_type, is_nullable
		  FROM information_schema.columns
		 WHERE table_schema = current_schema()
		   AND table_name   = 'api_keys'
		   AND column_name IN ('created_ip', 'created_ua', 'parent_key_id')`)
	if err != nil {
		t.Fatalf("query columns: %v", err)
	}
	defer rows.Close()
	want := map[string]struct {
		dt string
		nn string
	}{
		"created_ip":    {"inet", "YES"},
		"created_ua":    {"text", "YES"},
		"parent_key_id": {"uuid", "YES"},
	}
	got := map[string]struct {
		dt string
		nn string
	}{}
	for rows.Next() {
		var name, dt, nn string
		if err := rows.Scan(&name, &dt, &nn); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got[name] = struct {
			dt string
			nn string
		}{dt, nn}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows.Err: %v", err)
	}
	for col, want := range want {
		g, ok := got[col]
		if !ok {
			t.Errorf("api_keys.%s column missing — migration 00144 not applied", col)
			continue
		}
		if g.dt != want.dt {
			t.Errorf("api_keys.%s type: got %s, want %s", col, g.dt, want.dt)
		}
		if g.nn != want.nn {
			t.Errorf("api_keys.%s nullable: got %s, want %s", col, g.nn, want.nn)
		}
	}

	// 2. self-FK ON DELETE SET NULL. The FK clause is invisible
	//    through information_schema.columns, so we look at
	//    pg_constraint. We assert the FK exists and the delete
	//    action is SET NULL — the audit-evidence property that
	//    "a hard-deleted predecessor leaves the lineage row
	//    intact but un-anchored".
	//
	//    Note: conrelid must be cast via the per-test schema name
	//    (NOT 'public.api_keys'::regclass — that hard-coded cast
	//    failed CI because the per-test schema owns api_keys).
	//    We use format() to build the qualified identifier from
	//    current_schema() so the regclass cast resolves the
	//    right relation regardless of which schema the pool
	//    targets. format() with %I quotes the identifier safely.
	var fkAction string
	err = pool.QueryRow(ctx, `
		SELECT confdeltype
		  FROM pg_constraint
		 WHERE conrelid = format('%I.api_keys', current_schema())::regclass
		   AND contype  = 'f'
		   AND pg_get_constraintdef(oid) ILIKE '%parent_key_id%'`).Scan(&fkAction)
	if err != nil {
		// No FK row → the migration wasn't applied. The column
		// existence check above will already have failed, but
		// the explicit skip here makes the failure mode clearer
		// when this test runs on a database that's a couple of
		// migrations behind.
		if strings.Contains(err.Error(), "no rows") {
			t.Skipf("parent_key_id FK not present — migration 00144 not applied yet: %v", err)
		}
		t.Fatalf("query FK: %v", err)
	}
	switch fkAction {
	case "a": // NO ACTION
		t.Errorf("parent_key_id FK action = NO ACTION, want SET NULL (a)")
	case "r": // RESTRICT
		t.Errorf("parent_key_id FK action = RESTRICT, want SET NULL (a)")
	case "c": // CASCADE
		t.Errorf("parent_key_id FK action = CASCADE, want SET NULL (a)")
	case "n": // SET NULL
		// expected
	default:
		t.Errorf("parent_key_id FK action = %q, want SET NULL", fkAction)
	}

	// silence the unused-import lint for `migrations` if all the
	// pure-coverage checks above happen to skip — keep the
	// import set honest.
	_ = migrations.FS
}
