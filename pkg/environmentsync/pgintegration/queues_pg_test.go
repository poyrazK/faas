package pgintegration_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPGEnvironmentGitOpsQueueSQLGuardsAndIdentity(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	store, source, _, app, original := queueIntentFixture(t, state.NewPgStore(pool), "enforce")
	adoptQueueIntent(t, store, source)
	queueIntentWorker(t, store)
	for _, query := range []string{
		`update queue_bindings set max_concurrency=9 where id=$1`,
		`update queue_bindings set name='replacement' where id=$1`,
		`update queue_bindings set retired_at=now(),enabled=false where id=$1`,
		`update triggers set enabled=false where queue_binding_id=$1`,
		`update triggers set config='{}' where queue_binding_id=$1`,
	} {
		_, err := pool.Exec(t.Context(), query, original.Binding.ID)
		var pgerr *pgconn.PgError
		if !errors.As(err, &pgerr) || pgerr.ConstraintName != "environment_gitops_field_owned" {
			t.Fatalf("SQL ownership gate: %s: %v", query, err)
		}
	}

	if _, err := pool.Exec(t.Context(), `update triggers set id=gen_random_uuid() where queue_binding_id=$1`, original.Binding.ID); err == nil {
		t.Fatal("consumer UUID could be replaced")
	}
	// A token that was never issued cannot authorize an owned write.
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err := tx.Exec(t.Context(), `select set_config('gregale.gitops_lease','not-an-issued-lease',true)`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(t.Context(), `update queue_bindings set max_concurrency=9 where id=$1`, original.Binding.ID); err == nil {
		t.Fatal("unissued lease accepted")
	}
	if err := tx.Rollback(t.Context()); err != nil {
		t.Fatal(err)
	}
	current, err := store.EnvironmentGitSource(t.Context(), source.AccountID, source.ProjectID, "production")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `update queue_bindings set updated_at=now() where id=$1`, original.Binding.ID); err != nil {
		t.Fatal(err)
	}
	after, err := store.EnvironmentGitSource(t.Context(), source.AccountID, source.ProjectID, "production")
	if err != nil || after.IntentVersion != current.IntentVersion {
		t.Fatalf("runtime timestamp reported drift: %+v %v", after, err)
	}
	// An identity mapping cannot adopt a neighboring environment or replace the original UUID.
	var neighbor string
	if err := pool.QueryRow(t.Context(), `select id from queue_bindings where app_id=$1 and deployment_scope='staging'`, app.ID).Scan(&neighbor); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `update environment_gitops_queue_bindings set binding_id=$2 where source_id=$1`, source.ID, neighbor); err == nil {
		t.Fatal("neighbor identity adopted")
	}
}

func TestPGEnvironmentGitOpsQueueAdoptionSerializesWithAPIWriter(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	store, source, _, app, original := queueIntentFixture(t, state.NewPgStore(pool), "enforce")
	preview, err := store.PreviewEnvironmentGitOpsAdoption(t.Context(), source.AccountID, source.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Hold source-first, as adoption does, while the API writer reaches its lock.
	holder, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = holder.Rollback(context.Background()) }()
	if _, err := holder.Exec(t.Context(), `select id from environment_git_sources where id=$1 for update`, source.ID); err != nil {
		t.Fatal(err)
	}
	config := pool.Config().Copy()
	name := "gitops-queue-adoption-writer"
	config.ConnConfig.RuntimeParams["application_name"] = name
	writer, err := pgxpool.NewWithConfig(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() {
		cap := 2
		_, err := state.NewPgStore(writer).UpdateQueueBindingWithConsumer(ctx, source.AccountID, app.ID, original.Binding.ID, state.UpdateQueueBindingParams{MaxConcurrency: &cap})
		result <- err
	}()
	for {
		var waiting bool
		if err := pool.QueryRow(ctx, `select exists(select 1 from pg_stat_activity where application_name=$1 and wait_event_type='Lock')`, name).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		select {
		case err := <-result:
			t.Fatalf("writer bypassed source lock: %v", err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
	if err := holder.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	if err := store.AdoptEnvironmentGitOps(ctx, source.AccountID, source.ID, preview.Hash); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("review predating API commit accepted: %v", err)
	}
	// The rejection must preserve both the API value and lack of ownership.
	cap := 4
	if _, err := store.UpdateQueueBindingWithConsumer(ctx, source.AccountID, app.ID, original.Binding.ID, state.UpdateQueueBindingParams{MaxConcurrency: &cap}); err != nil {
		t.Fatalf("failed adoption transferred ownership: %v", err)
	}
}

func TestPGEnvironmentGitOpsQueueProjectionDriftCannotConverge(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	store, source, _, _, original := queueIntentFixture(t, state.NewPgStore(pool), "enforce")
	adoptQueueIntent(t, store, source)
	queueIntentWorker(t, store)
	current, err := store.EnvironmentGitSource(t.Context(), source.AccountID, source.ProjectID, "production")
	if err != nil {
		t.Fatal(err)
	}
	source, err = store.UpdateEnvironmentGitSource(t.Context(), source.AccountID, source.ID, state.EnvironmentGitSourceUpdate{ExpectedGeneration: current.Generation, Mode: "report"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `update triggers set filter_criteria='{"payload":[{"op":"eq","field":"kind","value":"hidden"}]}' where queue_binding_id=$1`, original.Binding.ID); err != nil {
		t.Fatal(err)
	}
	// Binding values still equal Git, but its private consumer's delivery filter does not.
	queueIntentWorker(t, store)
	runs, err := store.ListEnvironmentGitOpsRuns(t.Context(), source.AccountID, source.ID, 1)
	if err != nil || len(runs) != 1 || runs[0].Status != "blocked" || !strings.Contains(string(runs[0].Plan), "projection requires repair") {
		t.Fatalf("projection drift falsely converged: %+v %v", runs, err)
	}
}
