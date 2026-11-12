//go:build !no_pg

// adr: 20261104201200000
package state_test

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgStageAdmissionMigrationBackfill(t *testing.T) {
	ctx := t.Context()
	pool := pgtest.OpenMigrated(t)
	store := state.NewPgStore(pool)
	account, project, app := environmentLifecycleFixture(t, store)
	lifecycleEnvironment(t, store, account, project, "staging")
	settings, err := state.WorkloadSettingsFromApp(app)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.PutProjectEnvironmentWorkloadSpec(ctx, account.ID, project.ID, "staging", app.ID, 0, settings); err != nil {
		t.Fatal(err)
	}
	raw, err := migrations.FS.ReadFile("20261104202808784_project_stage_admissions.sql")
	if err != nil {
		t.Fatal(err)
	}
	up, down, found := strings.Cut(string(raw), "-- +goose Down")
	if !found {
		t.Fatal("stage admission migration lacks rollback")
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// Recreate the N-1 schema while retaining its existing desired stage head.
	if _, err := tx.Exec(ctx, down); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, up); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM project_app_stage_admissions WHERE app_id=$1 AND project_id=$2`, app.ID, project.ID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("existing stage head was not backfilled: count=%d err=%v", count, err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteProjectEnvironment(ctx, account.ID, project.ID, "staging"); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM project_release_environment_apps($1,'production')`, project.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("checked release membership lost the backfilled stage origin: count=%d err=%v", count, err)
	}
}
