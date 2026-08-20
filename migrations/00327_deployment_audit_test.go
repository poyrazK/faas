//go:build !no_pg

// Migration-apply test for 00327 (deployment_audit table — issue
// #976 / ADR-122 / SAFE-RELEASES-E.2). Pins the contract:
//  1. The migration set applies cleanly through 00320.
//  2. The deployment_audit table lands with the expected column shape
//     and nullable rules.
//  3. The closed-set deployment_audit_kind_chk CHECK covers the 8
//     kinds the meterd orchestrator emits.
//  4. The (deployment_id, at DESC) timeline index lands.
//  5. The 90-day GC partial index lands with the WHERE clause.
//  6. deployment_id has NO FK to deployments(id) (audit rows must
//     outlive the deployment row — mirrors audit_log / accounts
//     precedent from migration 00163 / issue #755 / PR-5).
//  7. account_id has NO FK to accounts(id) (same rationale).
//  8. Re-running goose MigrateUp is a no-op (replay safety).
//
// Build tag mirrors 00318_deployments_actor_test.go: set
// FAAS_SKIP_PG_TESTS=1 locally to skip.

package migrations_test

import (
	"context"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestMigrations_00320_DeploymentAudit(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)

	// (1) Run the full migration set. 00320 should land last.
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("db.MigrateUp: %v (PR follow-up failure mode: missing migration slot before 00320)", err)
	}

	// (2) Column shape. Scoped to current_schema() per
	// migrations-info-schema-scoping-pattern.md so a parallel
	// pgtest run on the same box doesn't bleed rows in.
	rows, err := pool.Query(ctx, `
		select column_name, data_type, is_nullable
		  from information_schema.columns
		 where table_schema = current_schema()
		   and table_name = 'deployment_audit'
		   and column_name in ('id', 'deployment_id', 'account_id',
		                       'kind', 'actor', 'at', 'data')`)
	if err != nil {
		t.Fatalf("query columns: %v", err)
	}
	defer rows.Close()
	colTypes := map[string]string{}
	colNullable := map[string]string{}
	for rows.Next() {
		var name, typ, nullable string
		if err := rows.Scan(&name, &typ, &nullable); err != nil {
			t.Fatalf("scan column row: %v", err)
		}
		colTypes[name] = typ
		colNullable[name] = nullable
	}
	wantCols := map[string]string{
		"id":            "bigint",
		"deployment_id": "uuid",
		"account_id":    "uuid",
		"kind":          "text",
		"actor":         "text",
		"at":            "timestamp with time zone",
		"data":          "jsonb",
	}
	for col, want := range wantCols {
		if colTypes[col] != want {
			t.Errorf("%s type = %q, want %q", col, colTypes[col], want)
		}
	}
	wantNullable := map[string]string{
		"deployment_id": "NO",
		"account_id":    "YES", // nullable: anonymous / post-deletion survival
		"kind":          "NO",
		"actor":         "NO",
		"at":            "NO",
		"data":          "YES", // nullable: empty audit rows are valid
	}
	for col, want := range wantNullable {
		if colNullable[col] != want {
			t.Errorf("%s nullable = %q, want %q", col, colNullable[col], want)
		}
	}

	// (3) Closed-set CHECK on `kind`. pg_get_constraintdef emits
	// either IN (a, b, c) or ANY(ARRAY[a, b, c]) per
	// pg-get-constraintdef-shapes.md; we assert each closed-set
	// value is present.
	var kindDef string
	err = pool.QueryRow(ctx, `
		select pg_get_constraintdef(c.oid)
		  from pg_constraint c
		  join pg_namespace n on n.oid = c.connamespace
		 where c.conname = 'deployment_audit_kind_chk'
		   and n.nspname = current_schema()`).Scan(&kindDef)
	if err != nil {
		t.Fatalf("query deployment_audit_kind_chk: %v (closed-set CHECK must have landed)", err)
	}
	for _, want := range []string{
		"deploy.created",
		"deploy.source_ref",
		"deploy.local_tarball",
		"deploy.traffic_changed",
		"deploy.health_probe_failed",
		"deploy.health_recovered",
		"deploy.rolled_back",
		"deploy.removed",
	} {
		if !strings.Contains(kindDef, want) {
			t.Errorf("deployment_audit_kind_chk def %q missing closed-set value %q", kindDef, want)
		}
	}

	// (4) Timeline index (deployment_id, at DESC) — the
	// dashboard-default sort order.
	var timelineIdx string
	err = pool.QueryRow(ctx, `
		select indexdef
		  from pg_indexes
		 where schemaname = current_schema()
		   and tablename = 'deployment_audit'
		   and indexname = 'deployment_audit_deployment_idx'`).Scan(&timelineIdx)
	if err != nil {
		t.Fatalf("query deployment_audit_deployment_idx: %v (timeline index must have landed)", err)
	}
	if !strings.Contains(timelineIdx, "(deployment_id, at DESC)") {
		t.Errorf("deployment_audit_deployment_idx def %q missing (deployment_id, at DESC)", timelineIdx)
	}

	// (5) 90-day GC partial index. ADR-122 §Consequences fixes
	// retention at 90 days; the migration must encode that in
	// the WHERE clause so a future retention change is an
	// explicit migration, not a silent SQL drift.
	var gcIdx string
	err = pool.QueryRow(ctx, `
		select indexdef
		  from pg_indexes
		 where schemaname = current_schema()
		   and tablename = 'deployment_audit'
		   and indexname = 'deployment_audit_at_gc_idx'`).Scan(&gcIdx)
	if err != nil {
		t.Fatalf("query deployment_audit_at_gc_idx: %v (GC index must have landed)", err)
	}
	if !strings.Contains(gcIdx, "90 days") {
		t.Errorf("deployment_audit_at_gc_idx def %q missing 90-day retention predicate", gcIdx)
	}

	// (6) deployment_id has NO FK to deployments(id). Mirror the
	// audit_log / accounts precedent (migration 00163, issue #755
	// / PR-5) — a 90-day-retention deployment row can be deleted
	// while the audit row remains. The test asserts ZERO
	// constraints on deployment_audit.deployment_id.
	var fkCount int
	err = pool.QueryRow(ctx, `
		select count(*)
		  from pg_constraint c
		  join pg_namespace n on n.oid = c.connamespace
		  join pg_class cls on cls.oid = c.conrelid
		  join pg_attribute a on a.attrelid = cls.oid
		                        and a.attnum = any(c.conkey)
		 where n.nspname = current_schema()
		   and cls.relname = 'deployment_audit'
		   and a.attname = 'deployment_id'
		   and c.contype = 'f'`).Scan(&fkCount)
	if err != nil {
		t.Fatalf("count FKs on deployment_audit.deployment_id: %v", err)
	}
	if fkCount != 0 {
		t.Errorf("deployment_audit.deployment_id has %d FK constraint(s), want 0 (audit rows must outlive the deployment)", fkCount)
	}

	// (7) account_id has NO FK to accounts(id). Same rationale —
	// a GDPR-erased accounts row does not cascade a
	// deployment_audit row.
	err = pool.QueryRow(ctx, `
		select count(*)
		  from pg_constraint c
		  join pg_namespace n on n.oid = c.connamespace
		  join pg_class cls on cls.oid = c.conrelid
		  join pg_attribute a on a.attrelid = cls.oid
		                        and a.attnum = any(c.conkey)
		 where n.nspname = current_schema()
		   and cls.relname = 'deployment_audit'
		   and a.attname = 'account_id'
		   and c.contype = 'f'`).Scan(&fkCount)
	if err != nil {
		t.Fatalf("count FKs on deployment_audit.account_id: %v", err)
	}
	if fkCount != 0 {
		t.Errorf("deployment_audit.account_id has %d FK constraint(s), want 0 (audit rows must outlive account deletion)", fkCount)
	}

	// (8) Replay safety: applying the migration set a second time
	// must not blow up. The CREATE TABLE IF NOT EXISTS / DROP
	// CONSTRAINT IF EXISTS guards handle this.
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("replay db.MigrateUp: %v (the CREATE TABLE IF NOT EXISTS / DROP CONSTRAINT IF EXISTS guards must have been silently dropped)", err)
	}
}
