//go:build !no_pg

// Migration-apply test for 00271 (issue #911 / ADR-110 PR-3a:
// release-bundle storage on compute_nodes).
//
// Pins the load-bearing contract that downstream PRs (PR-3 release
// bundle content + install, PR-4 gregale doctor) consume:
//
//   1. The migration set applies cleanly through 00271.
//   2. compute_nodes gains six nullable columns: release_id, manifest_hash,
//      host_certificate, cert_fingerprint, role, generation. Five are text;
//      generation is integer.
//   3. The columns are nullable: pre-PR-3a rows (the seeded default-local
//      from 00024 plus operator-added rows) accept the schema without a
//      backfill transaction.
//   4. INSERT a row that omits the six columns still succeeds — the
//      load-bearing contract that lets operator-added rows accept the
//      PR-3a schema without a one-time UPDATE.
//   5. COMMENT ON COLUMN statements are present for all six columns —
//      a "release_bundles storage carrier" header on the schema is what
//      PR-4 doctor and PR-3 bundle install rely on for context.
//
// Build tag mirrors 00072_compute_nodes_region_zone_test.go:1 —
// FAAS_SKIP_PG_TESTS=1 locally skips.

package migrations_test

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestMigrations_00271_ComputeNodesRelease(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("db.MigrateUp: %v", err)
	}

	// (1) Six columns present with correct type + nullability. The
	//     generation column is the only integer; the rest are text.
	type colSpec struct {
		name     string
		dataType string // "text" or "integer"
	}
	want := []colSpec{
		{"release_id", "text"},
		{"manifest_hash", "text"},
		{"host_certificate", "text"},
		{"cert_fingerprint", "text"},
		{"role", "text"},
		{"generation", "integer"},
	}
	for _, c := range want {
		var dataType, nullable string
		if err := pool.QueryRow(ctx, `
			select data_type, is_nullable
			  from information_schema.columns
			 where table_schema = current_schema()
			   and table_name   = 'compute_nodes'
			   and column_name  = $1
		`, c.name).Scan(&dataType, &nullable); err != nil {
			t.Errorf("compute_nodes.%s not present after 00271 apply: %v", c.name, err)
			continue
		}
		if dataType != c.dataType {
			t.Errorf("compute_nodes.%s data_type = %q, want %q", c.name, dataType, c.dataType)
		}
		// Nullable so pre-PR-3a compute_nodes rows accept the schema
		// without a backfill transaction. Doctor + bundle install
		// write NULL until populated.
		if nullable != "YES" {
			t.Errorf("compute_nodes.%s is_nullable = %q, want YES (nullable so pre-PR-3a rows accept the schema)", c.name, nullable)
		}
	}

	// (2) COMMENT ON COLUMN is present for all six columns. The
	//     comments are the PR-3a storage-carrier contract: future
	//     PR-3 / PR-4 readers reference the comment for context.
	//     Strip the prose (anything between ' -- ' and the next ')'
	//     isn't load-bearing; only column-level COMMENT is).
	for _, c := range want {
		var hasComment bool
		if err := pool.QueryRow(ctx, `
			select coalesce(length(col_description(c.oid, a.attnum)), 0) > 0
			  from pg_attribute a
			  join pg_class c on c.oid = a.attrelid
			 where c.relname = 'compute_nodes'
			   and a.attname = $1
			   and a.attnum  > 0
			   and not a.attisdropped
		`, c.name).Scan(&hasComment); err != nil {
			t.Errorf("lookup COMMENT on compute_nodes.%s: %v", c.name, err)
			continue
		}
		if !hasComment {
			t.Errorf("compute_nodes.%s has no COMMENT ON COLUMN (PR-3a storage-carrier contract)", c.name)
		}
	}

	// (3) INSERT a row that omits all six columns — must succeed
	//     under nullable. The load-bearing contract that lets
	//     operator-added rows accept the PR-3a schema without a
	//     one-time UPDATE.
	if _, err := pool.Exec(ctx, `
		insert into compute_nodes
			(name, target_url, vpcpus, mem_mb, max_concurrency, admission_ceiling_mb, lifecycle) values ('000271-no-release-row', 'tcp://127.0.0.1:1', 1, 256, 1, 256, 'active'::compute_node_lifecycle)
	`); err != nil {
		t.Errorf("insert compute_nodes with NULL release columns (must succeed under nullable): %v", err)
	}

	// (4) Smoke-test the integer column accepts a non-null value
	//     end-to-end. generation is the only integer; the rest are
	//     text. Confirms the schema applies to a fully-populated
	//     row (the bundle install path).
	var generation int
	if err := pool.QueryRow(ctx, `
		update compute_nodes
		   set generation = $2
		 where name = $1
		returning generation
	`, "000271-no-release-row", 7).Scan(&generation); err != nil {
		t.Errorf("update compute_nodes.generation = 7 (integer column round-trip): %v", err)
	} else if generation != 7 {
		t.Errorf("compute_nodes.generation round-trip = %d, want 7", generation)
	}
}
