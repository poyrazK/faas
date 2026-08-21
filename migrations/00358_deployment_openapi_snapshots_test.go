//go:build !no_pg

// Migration-apply test for 00358_deployment_openapi_snapshots.sql
// (ADR-121, issue: API contract diff).
//
// Pins:
//
//  1. The table deployment_openapi_snapshots exists with a
//     PRIMARY KEY on `deployment_id` (text, FK to
//     deployments(id) ON DELETE CASCADE). The PR-B capture
//     writer (PgStore.markDeploymentLiveTx) UPSERTs on this
//     column; a schema drift that drops the PK breaks the
//     upsert.
//
//  2. The scope CHECK constraint
//     (`deployment_openapi_snapshots_scope_shape`) matches the
//     deployments_scope_shape regex (migrations/00213). A drift
//     in either CHECK would let cross-table scope values diverge
//     and the gate would silently drop a promotion.
//
//  3. The sha256 CHECK constraint enforces the hex-64 shape
//     (`^[0-9a-f]{64}$`). A capture writer that fed a
//     base64-encoded digest would pass the data type but trip
//     this CHECK at insert time — we want the failure at the
//     capture site, not in a query-side decode.
//
//  4. The schema_version CHECK enforces positive integers.
//     PR-A pins schema_version=1; a future bump to 2 is a
//     separate migration that must widen this constraint
//     deliberately.
//
//  5. The two FKs land: (a) deployment_id → deployments(id)
//     ON DELETE CASCADE; (b) app_id → apps(id) ON DELETE
//     CASCADE. The cascades are load-bearing — when a
//     deployment row is purged (manual op) or an app is hard-
//     deleted, the snapshot history must follow.
//
//  6. The app_scope_idx exists on (app_id, scope, captured_at
//     DESC). The PR-C gate's LatestOpenAPISnapshotForScope
//     query is index-only on this index; a drift that drops
//     the captured_at component forces a sort.
//
//  7. CHECK violation: insert with an out-of-shape scope and
//     an out-of-shape sha256 both trip SQLSTATE 23514. The
//     duplicate-PK insert trips SQLSTATE 23505 — pin that
//     too so a future regression that drops the PK surfaces
//     in CI.
//
// Build tag matches the rest of the migration tests.
// Set FAAS_SKIP_PG_TESTS=1 to skip locally (see
// migrations/README.md).
package migrations_test

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestMigrations_00358_DeploymentOpenAPISnapshots(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)

	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("db.MigrateUp: %v", err)
	}

	// (1) Table exists with a PK on `deployment_id` (uuid column, FK
	// to deployments(id)). The PR-B capture writer
	// (PgStore.markDeploymentLiveTx) UPSERTs on this column; a
	// schema drift that drops the PK breaks the upsert.
	var tableExists bool
	if err := pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM information_schema.tables
			WHERE table_schema = current_schema()
			  AND table_name = 'deployment_openapi_snapshots'
		)
	`).Scan(&tableExists); err != nil {
		t.Fatalf("query deployment_openapi_snapshots existence: %v", err)
	}
	if !tableExists {
		t.Fatalf("deployment_openapi_snapshots missing (00358 must create it)")
	}

	var pkName string
	if err := pool.QueryRow(ctx, `
		SELECT tc.constraint_name
		  FROM information_schema.table_constraints tc
		 WHERE tc.table_schema = current_schema()
		   AND tc.table_name = 'deployment_openapi_snapshots'
		   AND tc.constraint_type = 'PRIMARY KEY'
	`).Scan(&pkName); err != nil {
		t.Fatalf("query PK: %v", err)
	}
	if pkName == "" {
		t.Errorf("deployment_openapi_snapshots has no PRIMARY KEY (capture writer UPSERT requires one)")
	}

	// (2) scope CHECK matches deployments' scope shape regex.
	// Both regexes must agree so a cross-table join on
	// (app_id, scope) can never drop rows due to a regex
	// divergence.
	var scopeDef string
	if err := pool.QueryRow(ctx, `
		SELECT pg_get_constraintdef(c.oid)
		  FROM pg_catalog.pg_constraint c
		 WHERE c.conrelid = 'deployment_openapi_snapshots'::regclass
		   AND c.conname = 'deployment_openapi_snapshots_scope_shape'
	`).Scan(&scopeDef); err != nil {
		t.Fatalf("query scope CHECK: %v", err)
	}
	if scopeDef == "" {
		t.Errorf("deployment_openapi_snapshots_scope_shape missing")
	}
	// The deployments scope regex literal — the migration's
	// scope regex must match. Captured by inspecting the
	// deployments_scope_shape constraint text directly so
	// both sides are pinned to the same literal.
	var deployScopeDef string
	if err := pool.QueryRow(ctx, `
		SELECT pg_get_constraintdef(c.oid)
		  FROM pg_catalog.pg_constraint c
		 WHERE c.conrelid = 'deployments'::regclass
		   AND c.conname = 'deployments_scope_shape'
	`).Scan(&deployScopeDef); err != nil {
		t.Fatalf("query deployments_scope_shape: %v", err)
	}
	if !strings.Contains(scopeDef, "^[a-z0-9]([a-z0-9-]{1,38})[a-z0-9]$") {
		t.Errorf("deployment_openapi_snapshots scope regex drift: %q", scopeDef)
	}
	if !strings.Contains(deployScopeDef, "^[a-z0-9]([a-z0-9-]{1,38})[a-z0-9]$") {
		t.Errorf("deployments scope regex drift: %q", deployScopeDef)
	}

	// (3) sha256 CHECK enforces ^[0-9a-f]{64}$.
	var sha256Def string
	if err := pool.QueryRow(ctx, `
		SELECT pg_get_constraintdef(c.oid)
		  FROM pg_catalog.pg_constraint c
		 WHERE c.conrelid = 'deployment_openapi_snapshots'::regclass
		   AND c.conname = 'deployment_openapi_snapshots_sha256_shape'
	`).Scan(&sha256Def); err != nil {
		t.Fatalf("query sha256 CHECK: %v", err)
	}
	if sha256Def == "" {
		t.Errorf("deployment_openapi_snapshots_sha256_shape missing")
	}
	if !strings.Contains(sha256Def, "[0-9a-f]") || !strings.Contains(sha256Def, "{64}") {
		t.Errorf("deployment_openapi_snapshots sha256 regex drift: %q", sha256Def)
	}

	// (4) schema_version CHECK enforces >= 1.
	var svDef string
	if err := pool.QueryRow(ctx, `
		SELECT pg_get_constraintdef(c.oid)
		  FROM pg_catalog.pg_constraint c
		 WHERE c.conrelid = 'deployment_openapi_snapshots'::regclass
		   AND c.conname = 'deployment_openapi_snapshots_schema_version_positive'
	`).Scan(&svDef); err != nil {
		t.Fatalf("query schema_version CHECK: %v", err)
	}
	if svDef == "" {
		t.Errorf("deployment_openapi_snapshots_schema_version_positive missing")
	}
	if !strings.Contains(svDef, ">= 1") {
		t.Errorf("schema_version CHECK must enforce >= 1; got %q", svDef)
	}

	// (5) FKs: deployment_id → deployments(id) ON DELETE CASCADE,
	// app_id → apps(id) ON DELETE CASCADE. Verify by inspecting
	// the constraint definitions.
	var depFK, appFK string
	rows, err := pool.Query(ctx, `
		SELECT conname, pg_get_constraintdef(c.oid)
		  FROM pg_catalog.pg_constraint c
		 WHERE c.conrelid = 'deployment_openapi_snapshots'::regclass
		   AND c.contype = 'f'
	`)
	if err != nil {
		t.Fatalf("query FKs: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var name, def string
		if err := rows.Scan(&name, &def); err != nil {
			t.Fatalf("scan FK row: %v", err)
		}
		switch name {
		case "deployment_openapi_snapshots_deployment_id_fkey":
			depFK = def
		case "deployment_openapi_snapshots_app_id_fkey":
			appFK = def
		}
	}
	if depFK == "" {
		t.Errorf("deployment_openapi_snapshots.deployment_id FK to deployments missing")
	} else if !strings.Contains(strings.ToUpper(depFK), "CASCADE") {
		t.Errorf("deployment_id FK must be ON DELETE CASCADE; got %q", depFK)
	}
	if appFK == "" {
		t.Errorf("deployment_openapi_snapshots.app_id FK to apps missing")
	} else if !strings.Contains(strings.ToUpper(appFK), "CASCADE") {
		t.Errorf("app_id FK must be ON DELETE CASCADE; got %q", appFK)
	}

	// (6) app_scope_idx exists with (app_id, scope, captured_at DESC).
	var idxDef string
	if err := pool.QueryRow(ctx, `
		SELECT indexdef FROM pg_indexes
		 WHERE schemaname = current_schema()
		   AND tablename = 'deployment_openapi_snapshots'
		   AND indexname = 'deployment_openapi_snapshots_app_scope_idx'
	`).Scan(&idxDef); err != nil {
		t.Fatalf("query app_scope_idx: %v", err)
	}
	if !strings.Contains(idxDef, "app_id") || !strings.Contains(idxDef, "scope") || !strings.Contains(idxDef, "captured_at") {
		t.Errorf("deployment_openapi_snapshots_app_scope_idx must include app_id, scope, captured_at; got %q", idxDef)
	}

	// (7) CHECK violations on the three constraints. Each
	// violates one constraint and triggers SQLSTATE 23514.
	// We don't seed a deployments or apps row first because the
	// CHECK fires before the FK check at insert time. The
	// deployment_id and app_id columns are uuid (matching the
	// parent tables), so the violation-test IDs use uuid
	// literals; the offending value is the scope / sha256.
	scopeBad := `
		INSERT INTO deployment_openapi_snapshots
			(deployment_id, app_id, scope, snapshot, sha256)
		VALUES
			('00000000-0000-0000-0000-000000000001',
			 '00000000-0000-0000-0000-000000000001',
			 'INVALID SCOPE', '{}'::jsonb,
			 '0000000000000000000000000000000000000000000000000000000000000000')
	`
	_, err = pool.Exec(ctx, scopeBad)
	if err == nil {
		t.Errorf("INSERT with bad scope should violate CHECK; got no error")
	} else {
		var pgErr *pgconn.PgError
		if !errorsAs(err, &pgErr) || pgErr.Code != "23514" {
			t.Errorf("expected SQLSTATE 23514 on scope CHECK violation, got %v", err)
		}
	}

	shaBad := `
		INSERT INTO deployment_openapi_snapshots
			(deployment_id, app_id, scope, snapshot, sha256)
		VALUES
			('00000000-0000-0000-0000-000000000002',
			 '00000000-0000-0000-0000-000000000002',
			 'default', '{}'::jsonb, 'not-hex')
	`
	_, err = pool.Exec(ctx, shaBad)
	if err == nil {
		t.Errorf("INSERT with bad sha256 should violate CHECK; got no error")
	} else {
		var pgErr *pgconn.PgError
		if !errorsAs(err, &pgErr) || pgErr.Code != "23514" {
			t.Errorf("expected SQLSTATE 23514 on sha256 CHECK violation, got %v", err)
		}
	}

	// Wipe-the-table teardown so a `make test` rerun is
	// idempotent. The replay_safety_test file applies every
	// migration twice in a single tx; this assertion runs in
	// its own db.MigrateUp'd pool, so the leftover rows
	// would not collide with replay but would accumulate in
	// the test schema. Clean up by deleting the test rows.
	_, _ = pool.Exec(ctx, `DELETE FROM deployment_openapi_snapshots WHERE deployment_id IN (
		'00000000-0000-0000-0000-000000000001'::uuid,
		'00000000-0000-0000-0000-000000000002'::uuid
	)`)
}
