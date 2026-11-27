//go:build !no_pg

// adr: 1740
package state_test

import (
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgFrozenThawGuards(t *testing.T) {
	store, _, _ := pgWithPool(t)
	testFrozenThawGuards(t, store)
}

func TestPgFrozenThawSelectionAgreesWithWake(t *testing.T) {
	store, _, _ := pgWithPool(t)
	testFrozenThawSelectionAgreesWithWake(t, store)
}

func TestPgAppRuntimeGeneration(t *testing.T) {
	store, _, _ := pgWithPool(t)
	testAppRuntimeGeneration(t, store)
}

// The thaw takes the app's runtime fences without waiting. A concurrent
// write holding the app row for a few milliseconds made it busy, and schedd
// parked the warm VM instead of thawing it (prod 2026-11-22). The thaw
// replays its transaction like every other publication (ADR-595).
func TestPgFrozenThawReplaysABusyRuntimeFence(t *testing.T) {
	store, _, pool := pgWithPool(t)
	node := runtimeSecretNodeForTest(t, store)
	_, thaw := seedFrozenThaw(t, store, node)
	ctx := t.Context()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Tx.Rollback is not safe for concurrent use: the timer and the deferred
	// cleanup share one rollback, and a late caller waits for the first.
	var rollbackOnce sync.Once
	rollback := func() { rollbackOnce.Do(func() { _ = tx.Rollback(ctx) }) }
	defer rollback()
	if _, err := tx.Exec(ctx, `SELECT id FROM apps WHERE id=$1 FOR UPDATE`, thaw.AppID); err != nil {
		t.Fatal(err)
	}
	release := time.AfterFunc(50*time.Millisecond, rollback)
	defer release.Stop()
	ins, err := store.ThawFrozenInstance(ctx, thaw)
	if err != nil || ins.State != string(state.StateRunning) {
		t.Fatalf("thaw behind a briefly held app row = %+v, %v; want running", ins, err)
	}
}
