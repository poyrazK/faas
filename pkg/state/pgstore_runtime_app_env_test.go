//go:build !no_pg

// adr: 566
package state_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgRuntimeAppEnvDeploymentScopeAndOwnership(t *testing.T) {
	store, _, _ := pgWithPool(t)
	testRuntimeAppEnvDeploymentScopeAndOwnership(t, store)
}

func TestPgRuntimeAppEnvStageLifetimeAndLegacy(t *testing.T) {
	store, _, _ := pgWithPool(t)
	testRuntimeAppEnvStageLifetimeAndLegacy(t, store)
}

func TestPgRuntimeAppEnvOwnerSurvivesPinLossAndRejectsReplacement(t *testing.T) {
	store, ctx, pool := pgWithPool(t)
	f := seedRuntimeAppEnv(t, store)
	dep := f.deployments["stage"]
	var specID, ownerID string
	if err := pool.QueryRow(ctx, `SELECT pin.spec_id::text,owner.environment_id::text
        FROM project_environment_workload_deployment_specs pin
        JOIN deployment_runtime_environment_owners owner USING(deployment_id) WHERE pin.deployment_id=$1`, dep.ID).Scan(&specID, &ownerID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM project_environment_workload_deployment_specs WHERE deployment_id=$1`, dep.ID); err != nil {
		t.Fatal(err)
	}
	if got, err := store.RuntimeAppEnvForDeployment(ctx, f.account.ID, f.app.ID, dep.ID); !errors.Is(err, state.ErrNotFound) || len(got.Values) != 0 {
		t.Fatalf("lost pin downgraded to legacy: %+v %v", got, err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO project_environment_workload_deployment_specs(deployment_id,spec_id) VALUES ($1,$2)`, dep.ID, specID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RuntimeAppEnvForDeployment(ctx, f.account.ID, f.app.ID, dep.ID); err != nil {
		t.Fatalf("same owner recovery: %v", err)
	}
	if err := store.DeleteProjectEnvironment(ctx, f.account.ID, f.project.ID, "stage"); err != nil {
		t.Fatal(err)
	}
	var retainedID string
	if err := pool.QueryRow(ctx, `SELECT environment_id::text FROM deployment_runtime_environment_owners WHERE deployment_id=$1`, dep.ID).Scan(&retainedID); err != nil || retainedID != ownerID {
		t.Fatalf("environment deletion discarded owner: %s %v", retainedID, err)
	}
	if _, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: f.account.ID, ProjectID: f.project.ID, Slug: "stage"}); err != nil {
		t.Fatal(err)
	}
	settings, err := state.WorkloadSettingsFromApp(f.app)
	if err != nil {
		t.Fatal(err)
	}
	replacement, err := store.PutProjectEnvironmentWorkloadSpec(ctx, f.account.ID, f.project.ID, "stage", f.app.ID, 0, settings)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO project_environment_workload_deployment_specs(deployment_id,spec_id) VALUES ($1,$2)`, dep.ID, replacement.ID); err == nil {
		t.Fatal("old deployment rebound to replacement environment")
	}
	if got, err := store.RuntimeAppEnvForDeployment(ctx, f.account.ID, f.app.ID, dep.ID); !errors.Is(err, state.ErrNotFound) || len(got.Values) != 0 {
		t.Fatalf("rejected rebind leaked values: %+v %v", got, err)
	}
	if _, err := store.ScheduleAppDeletion(ctx, f.app.ID, time.Now().UTC().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := store.ClaimAppDeletion(ctx, f.app.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteAppPermanently(ctx, f.app.ID); err != nil {
		t.Fatal(err)
	}
	var remaining int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM deployment_runtime_environment_owners WHERE deployment_id=$1`, dep.ID).Scan(&remaining); err != nil || remaining != 0 {
		t.Fatalf("deployment retention left an owner: %d %v", remaining, err)
	}
}

func TestPgRuntimeAppEnvReadsOneOwnershipAndValueSnapshot(t *testing.T) {
	store, ctx, pool := pgWithPool(t)
	f := seedRuntimeAppEnv(t, store)
	dep := f.deployments["stage"]
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `DELETE FROM project_environments WHERE account_id=$1 AND project_id=$2 AND slug='stage'`, f.account.ID, f.project.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO project_environments(account_id,project_id,slug) VALUES ($1,$2,'stage')`, f.account.ID, f.project.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE app_envs SET value='replacement-private' WHERE app_id=$1 AND scope='stage'`, f.app.ID); err != nil {
		t.Fatal(err)
	}
	got, err := store.RuntimeAppEnvForDeployment(ctx, f.account.ID, f.app.ID, dep.ID)
	if err != nil || len(got.Values) != 1 || got.Values[0].Value != "stage" {
		t.Fatalf("uncommitted replacement escaped: %+v %v", got, err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if got, err := store.RuntimeAppEnvForDeployment(ctx, f.account.ID, f.app.ID, dep.ID); !errors.Is(err, state.ErrNotFound) || len(got.Values) != 0 {
		t.Fatalf("committed replacement escaped old ownership: %+v %v", got, err)
	}
}

func TestPgRuntimeAppEnvOwnerMigrationBackfillAndRollbackFence(t *testing.T) {
	store, ctx, pool := pgWithPool(t)
	raw, err := migrations.FS.ReadFile("20261004095153175_deployment_runtime_environment_owners.sql")
	if err != nil {
		t.Fatal(err)
	}
	up, down, ok := strings.Cut(string(raw), "-- +goose Down")
	if !ok {
		t.Fatal("missing down migration")
	}
	if _, err := pool.Exec(ctx, down); err != nil {
		t.Fatal(err)
	}
	f := seedRuntimeAppEnv(t, store)
	if _, err := pool.Exec(ctx, up); err != nil {
		t.Fatal(err)
	}
	for _, dep := range f.deployments {
		if _, err := store.RuntimeAppEnvForDeployment(ctx, f.account.ID, f.app.ID, dep.ID); err != nil {
			t.Fatalf("existing pin not backfilled: %v", err)
		}
	}
	if _, err := pool.Exec(ctx, down); err == nil {
		t.Fatal("rollback discarded runtime lifetime owners")
	}
	if _, err := store.ScheduleAppDeletion(ctx, f.app.ID, time.Now().UTC().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := store.ClaimAppDeletion(ctx, f.app.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteAppPermanently(ctx, f.app.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, down); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, up); err != nil {
		t.Fatal(err)
	}
	coverage, err := store.ProjectEnvironmentCloneSchemaCoverage(ctx, f.account.ID, f.project.ID)
	if err != nil || !coverage.Known {
		t.Fatalf("runtime owner schema not registered: %+v %v", coverage.Blockers, err)
	}
}
