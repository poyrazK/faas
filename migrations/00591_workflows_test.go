//go:build !no_pg

package migrations_test

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestMigration_00591_Workflows(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	defer pool.Close()
	migrateUpOnce(ctx, t, pool)

	assertColumnShape(t, ctx, pool, "workflow_runs", "id", "uuid", "NO")
	assertColumnShape(t, ctx, pool, "workflow_runs", "app_id", "uuid", "NO")
	assertColumnShape(t, ctx, pool, "workflow_runs", "workflow_name", "text", "NO")
	assertColumnShape(t, ctx, pool, "workflow_runs", "status", "text", "NO")
	assertColumnShape(t, ctx, pool, "workflow_runs", "input", "jsonb", "NO")
	assertColumnShape(t, ctx, pool, "workflow_runs", "definition_snapshot", "jsonb", "NO")

	assertColumnShape(t, ctx, pool, "workflow_steps", "run_id", "uuid", "NO")
	assertColumnShape(t, ctx, pool, "workflow_steps", "step_name", "text", "NO")
	assertColumnShape(t, ctx, pool, "workflow_steps", "status", "text", "NO")
	assertColumnShape(t, ctx, pool, "workflow_steps", "attempt", "integer", "NO")

	assertColumnShape(t, ctx, pool, "workflow_events", "id", "uuid", "NO")
	assertColumnShape(t, ctx, pool, "workflow_events", "run_id", "uuid", "NO")
	assertColumnShape(t, ctx, pool, "workflow_events", "event_name", "text", "NO")
	assertColumnShape(t, ctx, pool, "workflow_events", "payload", "jsonb", "NO")

	// Verify replay safety: re-running MigrateUp must succeed without errors
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("replay MigrateUp: %v", err)
	}
}
