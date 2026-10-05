package migrations_test

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestWorkflowRunCreateIdempotencyMigration(t *testing.T) {
	pool := pgtest.Open(t)
	ctx := t.Context()
	if _, err := pool.Exec(ctx, `CREATE TABLE workflow_runs (
		id uuid PRIMARY KEY, app_id uuid NOT NULL, workflow_name text NOT NULL,
		status text NOT NULL, started_at timestamptz
	)`); err != nil {
		t.Fatal(err)
	}
	raw, err := migrations.FS.ReadFile("20261005120000000_workflow_run_create_idempotency.sql")
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
	if err := pool.QueryRow(ctx, `SELECT indexdef FROM pg_indexes WHERE indexname = 'workflow_runs_create_idempotency_idx'`).Scan(&indexDef); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(indexDef, "(app_id, workflow_name, create_idempotency_key)") || !strings.Contains(indexDef, "WHERE (create_idempotency_key IS NOT NULL)") {
		t.Fatalf("idempotency index definition = %q", indexDef)
	}

	appID := "00000000-0000-0000-0000-000000000001"
	firstRun := "00000000-0000-0000-0000-000000000011"
	secondRun := "00000000-0000-0000-0000-000000000012"
	if _, err := pool.Exec(ctx, `INSERT INTO workflow_runs (id, app_id, workflow_name, status, create_idempotency_key, create_request_fingerprint)
		VALUES ($1, $2, 'invoice', 'pending', 'request-1', decode(repeat('ab', 32), 'hex'))`, firstRun, appID); err != nil {
		t.Fatal("insert idempotency fixture", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO workflow_runs (id, app_id, workflow_name, status, create_idempotency_key)
		VALUES ($1, $2, 'invoice', 'pending', 'missing-fingerprint')`, "00000000-0000-0000-0000-000000000013", appID); err == nil {
		t.Fatal("key without request fingerprint passed the table constraint")
	}
	if _, err := pool.Exec(ctx, `INSERT INTO workflow_runs (id, app_id, workflow_name, status, create_idempotency_key, create_request_fingerprint)
		VALUES ($1, $2, 'invoice', 'pending', 'request-1', decode(repeat('cd', 32), 'hex'))`, secondRun, appID); err == nil {
		t.Fatal("duplicate key inserted twice")
	}
	if _, err := pool.Exec(ctx, sections[1]); err != nil {
		t.Fatal("rollback", err)
	}
	if _, err := pool.Exec(ctx, sections[0]); err != nil {
		t.Fatal("reapply", err)
	}
}
