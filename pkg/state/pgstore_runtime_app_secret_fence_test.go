//go:build !no_pg

// adr: 375
package state_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgRuntimeAppSecretObservationFence(t *testing.T) {
	store, _, _ := pgWithPool(t)
	testRuntimeAppSecretObservationFence(t, store)
}

func TestPgRuntimeAppSecretEmptyRevocationFence(t *testing.T) {
	store, _, _ := pgWithPool(t)
	testRuntimeAppSecretEmptyRevocationFence(t, store)
}

func TestPgRuntimeAppSecretBootDeliveryFence(t *testing.T) {
	store, _, _ := pgWithPool(t)
	testRuntimeAppSecretBootDeliveryFence(t, store)
}

func TestPgRuntimeAppSecretFenceWaitsForEnvironmentDeletion(t *testing.T) {
	testRuntimeAppSecretFenceWaitsForEnvironmentDeletion(t, false)
}

func TestPgRuntimeAppSecretBootDeliveryWaitsForEnvironmentDeletion(t *testing.T) {
	testRuntimeAppSecretFenceWaitsForEnvironmentDeletion(t, true)
}

func testRuntimeAppSecretFenceWaitsForEnvironmentDeletion(t *testing.T, boot bool) {
	store, ctx, pool := pgWithPool(t)
	f := seedRuntimeAppEnv(t, store)
	dep := f.deployments["stage"]
	instance, err := store.CreateInstance(ctx, f.app.ID, dep.ID, string(state.StateRunning), 256, runtimeSecretNodeForTest(t, store), "")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertAppSecretInScope(ctx, f.account.ID, f.app.ID, "stage", "TOKEN", []byte("original")); err != nil {
		t.Fatal(err)
	}
	result := state.AppSecretRuntimeReloadResult{AccountID: f.account.ID, AppID: f.app.ID, InstanceID: instance.ID,
		Fence: runtimeSecretFenceForTest(t, store, f, dep), Revision: strings.Repeat("a", 64),
		Projection: state.SecretReloadProjectionUpdated, Signal: state.SecretReloadSignalSent,
		Candidates: []state.AppSecretDeliveryCandidate{{Scope: "stage", Key: "TOKEN", Version: 1}}}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var blocker int
	if err := tx.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&blocker); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM project_environments WHERE id=$1`, result.Fence.EnvironmentID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO project_environments(account_id,project_id,slug) VALUES ($1,$2,'stage')`, f.account.ID, f.project.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE app_secrets SET ciphertext=$1 WHERE app_id=$2 AND scope='stage' AND key='TOKEN'`, []byte("replacement"), f.app.ID); err != nil {
		t.Fatal(err)
	}
	type outcome struct {
		count int
		err   error
	}
	done := make(chan outcome, 1)
	go func() {
		var count int
		var err error
		if boot {
			count, err = store.RecordAppSecretDelivery(ctx, state.AppSecretDeliveryResult{AccountID: result.AccountID, AppID: result.AppID,
				InstanceID: result.InstanceID, WakeID: instance.WakeID, Fence: result.Fence, Status: state.SecretDeliveryDelivered, Candidates: result.Candidates})
		} else {
			count, err = store.RecordAppSecretRuntimeReload(ctx, result)
		}
		done <- outcome{count, err}
	}()
	// Observe this writer blocked by this transaction, rather than inferring
	// synchronization from the time a goroutine happens to take to finish.
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
		case outcome := <-done:
			t.Fatalf("writer bypassed environment deletion lock: %+v", outcome)
		case <-deadline.C:
			t.Fatal("writer never reached the environment ownership lock")
		case <-tick.C:
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case outcome := <-done:
		if outcome.count != 0 || !errors.Is(outcome.err, state.ErrConflict) {
			t.Fatalf("writer adopted replacement lifetime: %+v", outcome)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("writer did not resume after deletion committed")
	}
	row, err := store.GetAppSecretInScope(ctx, f.account.ID, f.app.ID, "stage", "TOKEN")
	if err != nil || row.LastRuntimeReloadVersion != 0 || row.DeliveredVersion != 0 || string(row.Ciphertext) != "replacement" {
		t.Fatalf("stale observation modified replacement: %+v %v", row, err)
	}
}
