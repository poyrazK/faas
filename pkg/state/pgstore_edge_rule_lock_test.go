//go:build !no_pg

package state_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestPgStoreEdgeRuleMutationLockContendersDoNotStarveHolder — the edge-rule
// lock blocked in pg_advisory_lock while holding a pool connection, the
// pattern #3458 removed from the activation lock. Concurrent mutations for
// one app parked one connection each; with the pool full of waiters the
// holder — which needs the pool to write the rules and reach the gateway
// barrier — could not finish, so every apid request stalled.
func TestPgStoreEdgeRuleMutationLockContendersDoNotStarveHolder(t *testing.T) {
	base := pgtest.Open(t)
	cfg := base.Config()
	cfg.MaxConns = 3
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	store := state.NewPgStore(pool)
	appID := uuid.NewString()
	release, err := store.AcquireEdgeRuleMutationLock(ctx, appID)
	if err != nil {
		t.Fatal(err)
	}
	// Idempotent; deferred after pool.Close so a failed assertion still
	// frees the lock and lets the pool drain instead of hanging the test.
	defer release(ctx)

	// More contenders than the pool has connections.
	const contenders = 4
	var wg sync.WaitGroup
	errs := make(chan error, contenders)
	for i := 0; i < contenders; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := store.AcquireEdgeRuleMutationLock(ctx, appID)
			if err == nil {
				r(ctx)
			}
			errs <- err
		}()
	}
	time.Sleep(200 * time.Millisecond)

	// The holder still needs ordinary pool reads while it owns the lock.
	readCtx, readCancel := context.WithTimeout(ctx, time.Second)
	defer readCancel()
	var one int
	if err := pool.QueryRow(readCtx, `select 1`).Scan(&one); err != nil {
		t.Fatalf("lock holder could not read through the shared pool: %v", err)
	}
	release(ctx)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("contender: %v", err)
		}
	}
}

// TestPgStoreEdgeRuleMutationLockCancelledContenderLeavesNoLock — a contender
// whose context is cancelled while waiting must not leave the lock behind:
// the next mutation for the app acquires it immediately.
func TestPgStoreEdgeRuleMutationLockCancelledContenderLeavesNoLock(t *testing.T) {
	pool := pgtest.Open(t)
	store := state.NewPgStore(pool)
	appID := uuid.NewString()
	ctx := context.Background()
	release, err := store.AcquireEdgeRuleMutationLock(ctx, appID)
	if err != nil {
		t.Fatal(err)
	}
	waitCtx, waitCancel := context.WithTimeout(ctx, 150*time.Millisecond)
	defer waitCancel()
	if _, err := store.AcquireEdgeRuleMutationLock(waitCtx, appID); err == nil {
		t.Fatal("contender acquired a held lock")
	}
	release(ctx)
	release(ctx) // idempotent

	nextCtx, nextCancel := context.WithTimeout(ctx, time.Second)
	defer nextCancel()
	next, err := store.AcquireEdgeRuleMutationLock(nextCtx, appID)
	if err != nil {
		t.Fatalf("lock not free after holder release and cancelled contender: %v", err)
	}
	next(ctx)
}
