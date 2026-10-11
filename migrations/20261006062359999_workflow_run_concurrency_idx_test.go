package migrations_test

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestWorkflowRunConcurrencyIndexMigration(t *testing.T) {
	pool := pgtest.Open(t)
	ctx := t.Context()
	if _, err := pool.Exec(ctx, `CREATE TABLE workflow_runs (
		app_id uuid, workflow_name text, status text, started_at timestamptz
	)`); err != nil {
		t.Fatal(err)
	}
	raw, err := migrations.FS.ReadFile("20261006062359999_workflow_run_concurrency_idx.sql")
	if err != nil {
		t.Fatal(err)
	}
	sections := strings.SplitN(string(raw), "-- +goose Down", 2)
	for range 2 {
		if _, err := pool.Exec(ctx, sections[0]); err != nil {
			t.Fatal("apply/replay", err)
		}
	}
	var indexDef string
	if err := pool.QueryRow(ctx, `SELECT indexdef FROM pg_indexes WHERE indexname = 'workflow_runs_app_name_concurrency_idx'`).Scan(&indexDef); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(indexDef, "(app_id, workflow_name, status, started_at)") || !strings.Contains(indexDef, "pending") {
		t.Fatalf("concurrency index definition = %q", indexDef)
	}
	if _, err := pool.Exec(ctx, sections[1]); err != nil {
		t.Fatal("rollback", err)
	}
	if _, err := pool.Exec(ctx, sections[0]); err != nil {
		t.Fatal("reapply", err)
	}
}
