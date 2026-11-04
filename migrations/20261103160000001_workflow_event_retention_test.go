//go:build !no_pg

package migrations_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestWorkflowEventArchiveReapplyAndAppDeletion(t *testing.T) {
	pool := pgtest.Open(t)
	ctx := t.Context()
	if _, err := pool.Exec(ctx, `
		CREATE TABLE apps (id uuid PRIMARY KEY);
		CREATE TABLE workflow_runs (id uuid PRIMARY KEY, app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE);
		CREATE TABLE workflow_events (id uuid PRIMARY KEY, run_id uuid NOT NULL REFERENCES workflow_runs(id) ON DELETE CASCADE,
			event_name text NOT NULL, payload jsonb NOT NULL, received_at timestamptz NOT NULL);
	`); err != nil {
		t.Fatal(err)
	}
	raw, err := migrations.FS.ReadFile("20261103160000001_workflow_event_retention.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.SplitN(string(raw), "-- +goose Down", 2)
	if len(parts) != 2 {
		t.Fatal("missing migration down section")
	}
	apply := func(sql string) {
		t.Helper()
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback(ctx) }()
		if _, err := tx.Exec(ctx, sql); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
	}
	apply(parts[0])
	app, run, event := uuid.NewString(), uuid.NewString(), uuid.NewString()
	if _, err := pool.Exec(ctx, `INSERT INTO apps VALUES($1)`, app); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO workflow_runs VALUES($1,$2)`, run, app); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO workflow_events VALUES($1,$2,'ready','{}',now())`, event, run); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM workflow_runs WHERE id=$1`, run); err != nil {
		t.Fatal(err)
	}
	apply(parts[1])
	apply(parts[0])
	var archived, triggers int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM workflow_event_archive WHERE id=$1`, event).Scan(&archived); err != nil || archived != 1 {
		t.Fatalf("archive after reapply: %d, %v", archived, err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM pg_trigger WHERE tgrelid='workflow_runs'::regclass AND tgname='workflow_runs_archive_events' AND NOT tgisinternal`).Scan(&triggers); err != nil || triggers != 1 {
		t.Fatalf("archive triggers after reapply: %d, %v", triggers, err)
	}

	run, event = uuid.NewString(), uuid.NewString()
	if _, err := pool.Exec(ctx, `INSERT INTO workflow_runs VALUES($1,$2)`, run, app); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO workflow_events VALUES($1,$2,'ready','{}',now())`, event, run); err != nil {
		t.Fatal(err)
	}
	retention, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = retention.Rollback(context.Background()) }()
	purge, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = purge.Rollback(context.Background()) }()
	if _, err := retention.Exec(ctx, `SELECT id FROM workflow_runs WHERE id=$1 FOR UPDATE`, run); err != nil {
		t.Fatal(err)
	}
	if _, err := purge.Exec(ctx, `SELECT id FROM apps WHERE id=$1 FOR UPDATE`, app); err != nil {
		t.Fatal(err)
	}
	deadline, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	tag, err := retention.Exec(deadline, `DELETE FROM workflow_runs WHERE id=$1`, run)
	if err != nil || tag.RowsAffected() != 0 {
		t.Fatalf("retention should skip locked parent without waiting: %v, %v", tag, err)
	}
	if err := retention.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := purge.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM apps WHERE id=$1`, app); err != nil {
		t.Fatalf("app cascade must delete its runs and archive: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM workflow_event_archive WHERE app_id=$1`, app).Scan(&archived); err != nil || archived != 0 {
		t.Fatalf("app archive after deletion: %d, %v", archived, err)
	}
}
