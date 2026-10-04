//go:build !no_pg

// adr: 583
package state_test

import (
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgRuntimeScalingStateIsolation(t *testing.T) {
	store, _, _ := pgWithPool(t)
	testRuntimeScalingStateIsolation(t, store)
}

func TestPgRuntimeScalingStateLifetime(t *testing.T) {
	store, _, _ := pgWithPool(t)
	testRuntimeScalingStateLifetime(t, store)
}

func TestPgRuntimeScalingStateLegacyProduction(t *testing.T) {
	store, _, _ := pgWithPool(t)
	testRuntimeScalingStateLegacyProduction(t, store)
}

func TestPgRuntimeScalingStateMixedProductionGenerations(t *testing.T) {
	store, _, _ := pgWithPool(t)
	testRuntimeScalingStateMixedProductionGenerations(t, store)
}

func TestPgRuntimeScalingStateRejectsMissingOwnerOrPin(t *testing.T) {
	store, ctx, pool := pgWithPool(t)
	f := seedRuntimeAppEnv(t, store)
	dep := f.deployments["stage"]
	var specID string
	if err := pool.QueryRow(ctx, `SELECT spec_id::text FROM project_environment_workload_deployment_specs WHERE deployment_id=$1`, dep.ID).Scan(&specID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM project_environment_workload_deployment_specs WHERE deployment_id=$1`, dep.ID); err != nil {
		t.Fatal(err)
	}
	assertRejected := func() {
		t.Helper()
		if _, err := store.RuntimeScalingStateForDeployment(ctx, f.account.ID, f.app.ID, dep.ID); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("missing ownership read clocks: %v", err)
		}
		if err := store.StampDeploymentScaleOut(ctx, dep.ID); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("missing ownership wrote clocks: %v", err)
		}
	}
	assertRejected()
	if _, err := pool.Exec(ctx, `INSERT INTO project_environment_workload_deployment_specs(deployment_id,spec_id) VALUES($1,$2)`, dep.ID, specID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM deployment_runtime_environment_owners WHERE deployment_id=$1`, dep.ID); err != nil {
		t.Fatal(err)
	}
	assertRejected()
	var remaining int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM runtime_environment_scaling_states WHERE app_id=$1`, f.app.ID).Scan(&remaining); err != nil || remaining != 0 {
		t.Fatalf("invalid writer left history: %d %v", remaining, err)
	}
}

func TestPgRuntimeScalingStampWaitsForEnvironmentDeletion(t *testing.T) {
	store, ctx, pool := pgWithPool(t)
	f := seedRuntimeAppEnv(t, store)
	dep := f.deployments["stage"]
	env, err := store.ProjectEnvironmentBySlug(ctx, f.account.ID, f.project.ID, "stage")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.StampDeploymentScaleIn(ctx, dep.ID); err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var blocker int
	if err := tx.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&blocker); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM project_environments WHERE id=$1`, env.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO project_environments(account_id,project_id,slug) VALUES($1,$2,'stage')`, f.account.ID, f.project.ID); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- store.StampDeploymentScaleOut(ctx, dep.ID) }()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		var waiting bool
		if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE query LIKE '-- name: LockRuntimeSecretEnvironment%' AND $1=ANY(pg_blocking_pids(pid)))`, blocker).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("writer bypassed original environment lock: %v", err)
		case <-deadline.C:
			t.Fatal("writer never reached original environment lock")
		case <-tick.C:
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("writer adopted replacement: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("writer did not resume after deletion")
	}
	var remaining int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM runtime_environment_scaling_states WHERE app_id=$1`, f.app.ID).Scan(&remaining); err != nil || remaining != 0 {
		t.Fatalf("deleted environment history persisted: %d %v", remaining, err)
	}
	app, err := store.AppByID(ctx, f.app.ID)
	if err != nil || app.LastScaleInAt != nil || app.LastScaleOutAt != nil {
		t.Fatalf("stage writer changed production: %+v %v", app, err)
	}
}
