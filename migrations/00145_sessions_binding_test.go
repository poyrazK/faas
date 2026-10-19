//go:build !no_pg

package migrations_test

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

// TestMigrations_00145_SessionsBinding pins the IDEMPOTENT ALTER
// shape and column type of the sessions.binding_hash column landed
// by 00145_sessions_binding.sql (IAM-hardening-mega-PR logical
// change 5, ADR-076).
//
// What we pin (replay-safety + audit-evidence cost):
//   - Column type matches pkg/state.Session.BindingHash (TEXT —
//     unbounded per the migration; the live value is bounded
//     to 64 chars hex by pkg/bindinghash.Compute).
//   - The column is nullable (the "binding not armed" marker;
//     pre-PR rows have NULL and the middleware cross-check
//     short-circuits).
//   - The replay guard: re-running the migration after a
//     successful first run is a no-op (ADD COLUMN IF NOT EXISTS
//     in 00145).
//
// Cross-PR slot reservation note: this test is coupled to the .sql
// at slot 136. If a sibling PR renumbers, the test file moves with
// it (per migration-slot-renumber-at-pr-creation).
func TestMigrations_00145_SessionsBinding(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.OpenMigrated(t)

	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("db.MigrateUp: %v", err)
	}

	rows, err := pool.Query(ctx, `
		SELECT column_name, data_type, is_nullable
		  FROM information_schema.columns
		 WHERE table_schema = current_schema()
		   AND table_name   = 'sessions'
		   AND column_name  = 'binding_hash'`)
	if err != nil {
		t.Fatalf("query columns: %v", err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatalf("sessions.binding_hash column missing — migration 00145 not applied")
	}
	var name, dt, nn string
	if err := rows.Scan(&name, &dt, &nn); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if name != "binding_hash" {
		t.Errorf("column name: got %s, want binding_hash", name)
	}
	if dt != "text" {
		t.Errorf("binding_hash type: got %s, want text", dt)
	}
	if nn != "YES" {
		t.Errorf("binding_hash nullable: got %s, want YES", nn)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows.Err: %v", err)
	}
}
