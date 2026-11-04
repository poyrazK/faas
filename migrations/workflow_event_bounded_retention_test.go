//go:build !no_pg

package migrations_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestWorkflowEventBoundedRetentionMigratesExistingEvents(t *testing.T) {
	const version int64 = 20261104001702109
	ctx := t.Context()
	pool := pgtest.Open(t)
	migrateUpTo(t, ctx, pool, migrationVersionBefore(t, version))
	account := seedAccount(t, ctx, pool)
	app := seedApp(t, ctx, pool, account)
	var run, event string
	if err := pool.QueryRow(ctx, `INSERT INTO workflow_runs (app_id,workflow_name,status,definition_snapshot,finished_at)
		VALUES ($1,'legacy','succeeded','{}',now()) RETURNING id::text`, app).Scan(&run); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO workflow_events (run_id,event_name,payload)
		VALUES ($1,'ready','{}') RETURNING id::text`, run).Scan(&event); err != nil {
		t.Fatal(err)
	}
	migrateUpTo(t, ctx, pool, version)
	if _, err := pool.Exec(ctx, `UPDATE workflow_runs SET id=$2 WHERE id=$1`, run, uuid.NewString()); err == nil {
		t.Fatal("a legacy run with events changed its ID")
	}
	if _, err := pool.Exec(ctx, `DELETE FROM workflow_runs WHERE id=$1`, run); err != nil {
		t.Fatal(err)
	}
	raw, err := migrations.FS.ReadFile("20261104001702109_workflow_event_bounded_retention.sql")
	if err != nil {
		t.Fatal(err)
	}
	up, down, ok := strings.Cut(string(raw), "-- +goose Down")
	if !ok {
		t.Fatal("missing down migration")
	}
	for range 2 {
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(ctx, up); err != nil {
			_ = tx.Rollback(ctx)
			t.Fatalf("replay migration: %v", err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, down); err != nil {
		t.Fatalf("rollback compatibility: %v", err)
	}
	var live, owned int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM workflow_events WHERE id=$1),
		(SELECT count(*) FROM workflow_event_owners WHERE run_id=$2 AND deleted_at IS NOT NULL)`, event, run).Scan(&live, &owned); err != nil || live != 1 || owned != 1 {
		t.Fatalf("legacy event after run deletion: live=%d owner=%d err=%v", live, owned, err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM workflow_event_owners WHERE run_id=$1`, run); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM workflow_events WHERE id=$1`, event).Scan(&live); err != nil || live != 0 {
		t.Fatalf("owner deletion retained legacy event: %d, %v", live, err)
	}
}

func TestWorkflowEventAppDeletionDoesNotWaitForRunIDLock(t *testing.T) {
	ctx := t.Context()
	pool := pgtest.Open(t)
	if _, err := pool.Exec(ctx, `CREATE TABLE apps (id uuid PRIMARY KEY);
		CREATE TABLE workflow_runs (id uuid PRIMARY KEY, app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE);
		CREATE TABLE workflow_events (id uuid PRIMARY KEY, run_id uuid NOT NULL REFERENCES workflow_runs(id) ON DELETE CASCADE,
			event_name text NOT NULL, payload jsonb NOT NULL, received_at timestamptz NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"20261103160000001_workflow_event_retention.sql", "20261104001702109_workflow_event_bounded_retention.sql"} {
		raw, err := migrations.FS.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		up, _, ok := strings.Cut(string(raw), "-- +goose Down")
		if !ok {
			t.Fatalf("%s has no down migration", name)
		}
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(ctx, up); err != nil {
			_ = tx.Rollback(ctx)
			t.Fatal(err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
	}
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
	lock, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Rollback(ctx) }()
	if _, err := lock.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('workflow-event-owner/' || $1::text, 0))`, run); err != nil {
		t.Fatal(err)
	}
	deadline, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, err = pool.Exec(deadline, `DELETE FROM apps WHERE id=$1`, app)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "40001" || deadline.Err() != nil {
		t.Fatalf("app deletion should promptly request retry: %v", err)
	}
	if err := lock.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM apps WHERE id=$1`, app); err != nil {
		t.Fatalf("app deletion after ID unlock: %v", err)
	}
	var events, owners int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM workflow_events WHERE id=$1),
		(SELECT count(*) FROM workflow_event_owners WHERE run_id=$2)`, event, run).Scan(&events, &owners); err != nil || events != 0 || owners != 0 {
		t.Fatalf("app deletion retained event data: events=%d owners=%d err=%v", events, owners, err)
	}
}
