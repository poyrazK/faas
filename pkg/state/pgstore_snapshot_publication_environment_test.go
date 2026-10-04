//go:build !no_pg

// adr: 566
package state_test

import (
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgSnapshotPublicationEnvironmentOwnership(t *testing.T) {
	store, _, _ := pgWithPool(t)
	testSnapshotPublicationEnvironmentOwnership(t, store)
}

func TestPgSnapshotPublicationRequiresExactSourceStart(t *testing.T) {
	store, _, _ := pgWithPool(t)
	testSnapshotPublicationRequiresExactSourceStart(t, store)
}

func TestPgSnapshotPublicationWaitsForEnvironmentDeletion(t *testing.T) {
	store, ctx, pool := pgWithPool(t)
	f := seedRuntimeAppEnv(t, store)
	dep := f.deployments["stage"]
	env, err := store.ProjectEnvironmentBySlug(ctx, f.account.ID, f.project.ID, "stage")
	if err != nil {
		t.Fatal(err)
	}
	source, err := store.CreateInstance(ctx, f.app.ID, dep.ID, string(state.StateParked), 256, runtimeSecretNodeForTest(t, store), "")
	if err != nil {
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
	if _, err := tx.Exec(ctx, `INSERT INTO project_environments(account_id,project_id,slug) VALUES ($1,$2,'stage')`, f.account.ID, f.project.ID); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := store.PublishSnapshotIfRuntimeFresh(ctx, state.Snapshot{DeploymentID: dep.ID, FCVersion: "1.10", StorageKey: state.SnapshotCaptureMemKey(dep.ID, state.SnapshotTierInit, "racing-delete")}, source.ID, source.StartedAt)
		done <- err
	}()
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
			t.Fatalf("publication bypassed original environment lock: %v", err)
		case <-deadline.C:
			t.Fatal("publication never reached original environment lock")
		case <-tick.C:
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, state.ErrSnapshotRuntimeStale) {
			t.Fatalf("publication adopted recreated stage: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("publication did not resume after environment deletion")
	}
	if _, err := store.LatestSnapshotForTier(ctx, dep.ID, state.SnapshotTierInit); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("delayed capture became restorable: %v", err)
	}
}
