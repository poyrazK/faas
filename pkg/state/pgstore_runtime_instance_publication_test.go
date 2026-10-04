//go:build !no_pg

// adr: 581
package state_test

import (
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgRuntimeInstancePublicationOwnership(t *testing.T) {
	store, _, _ := pgWithPool(t)
	testRuntimeInstancePublicationOwnership(t, store)
}

func TestPgRuntimeInstancePublicationLifetime(t *testing.T) {
	store, _, _ := pgWithPool(t)
	testRuntimeInstancePublicationLifetime(t, store)
}

func TestPgWarmInstancePublicationOwnership(t *testing.T) {
	store, _, _ := pgWithPool(t)
	testWarmInstancePublicationOwnership(t, store)
}

func TestPgRuntimeInstancePublicationWaitsForOriginalDeletion(t *testing.T) {
	testPgRuntimeInstancePublicationWaitsForOriginalDeletion(t, false)
}

func TestPgWarmInstancePublicationWaitsForOriginalDeletion(t *testing.T) {
	testPgRuntimeInstancePublicationWaitsForOriginalDeletion(t, true)
}

func testPgRuntimeInstancePublicationWaitsForOriginalDeletion(t *testing.T, paused bool) {
	t.Helper()
	store, ctx, pool := pgWithPool(t)
	f := seedRuntimeAppEnv(t, store)
	p := seedRuntimeInstancePublication(t, store, f, f.deployments["stage"], runtimeSecretNodeForTest(t, store))
	if paused {
		if err := store.UpdateInstanceState(ctx, p.InstanceID, string(state.StateWaking)); err != nil {
			t.Fatal(err)
		}
		p.ExpectedState, p.TargetState = string(state.StateWaking), string(state.StateWarm)
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
	if _, err := tx.Exec(ctx, `DELETE FROM project_environments WHERE id=$1`, p.Fence.EnvironmentID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO project_environments(account_id,project_id,slug) VALUES($1,$2,'stage')`, f.account.ID, f.project.ID); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := store.PublishOwnedInstanceRuntime(ctx, p); done <- err }()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		var waiting bool
		if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity
			WHERE query LIKE '-- name: LockRuntimeSecretEnvironment%' AND $1=ANY(pg_blocking_pids(pid)))`, blocker).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("publication bypassed original environment lock: %v", err)
		case <-deadline.C:
			t.Fatal("publication did not reach environment lock")
		case <-tick.C:
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, state.ErrConflict) {
			t.Fatalf("publication adopted replacement: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("publication did not resume after original deletion")
	}
	assertRuntimePublicationUnchanged(t.Context(), t, store, p)
}

func TestPgRuntimeInstancePublicationRejectsMissingPin(t *testing.T) {
	store, ctx, pool := pgWithPool(t)
	f := seedRuntimeAppEnv(t, store)
	p := seedRuntimeInstancePublication(t, store, f, f.deployments["stage"], runtimeSecretNodeForTest(t, store))
	if _, err := pool.Exec(ctx, `DELETE FROM project_environment_workload_deployment_specs WHERE deployment_id=$1`, p.Fence.DeploymentID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PublishOwnedInstanceRuntime(ctx, p); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("lost pin published runtime: %v", err)
	}
	assertRuntimePublicationUnchanged(t.Context(), t, store, p)
}
