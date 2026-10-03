//go:build !no_pg

package state_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgStoreDeploymentActivationLockSerializesSubscribers(t *testing.T) {
	pool := pgtest.Open(t)
	first := state.NewPgStore(pool)
	second := state.NewPgStore(pool)
	deploymentID := uuid.NewString()
	ctx := context.Background()
	releaseFirst, err := first.AcquireDeploymentActivationLock(ctx, deploymentID)
	if err != nil {
		t.Fatal(err)
	}
	defer releaseFirst(ctx)

	acquired := make(chan error, 1)
	go func() {
		releaseSecond, err := second.AcquireDeploymentActivationLock(ctx, deploymentID)
		if err == nil {
			releaseSecond(ctx)
		}
		acquired <- err
	}()
	select {
	case err := <-acquired:
		t.Fatalf("second subscriber acquired lock early: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	releaseFirst(ctx)
	select {
	case err := <-acquired:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("second subscriber did not acquire after release")
	}
}

func TestPgStoreDeploymentActivationLockContenderDoesNotStarveHolder(t *testing.T) {
	base := pgtest.Open(t)
	cfg := base.Config()
	cfg.MaxConns = 3 // production imaged budget: one LISTEN and two handlers
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	listener, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Release()

	store := state.NewPgStore(pool)
	deploymentID := uuid.NewString()
	releaseFirst, err := store.AcquireDeploymentActivationLock(ctx, deploymentID)
	if err != nil {
		t.Fatal(err)
	}
	defer releaseFirst(ctx)

	acquired := make(chan error, 1)
	go func() {
		releaseSecond, err := store.AcquireDeploymentActivationLock(ctx, deploymentID)
		if err == nil {
			releaseSecond(ctx)
		}
		acquired <- err
	}()
	select {
	case err := <-acquired:
		t.Fatalf("contender acquired before holder released: %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	// The holder needs an ordinary read after taking its session lock. A
	// blocking lock attempt on the third connection deadlocks this read.
	readCtx, readCancel := context.WithTimeout(ctx, time.Second)
	defer readCancel()
	var one int
	if err := pool.QueryRow(readCtx, `select 1`).Scan(&one); err != nil {
		t.Fatalf("lock holder could not read through the shared pool: %v", err)
	}
	if one != 1 {
		t.Fatalf("read result = %d, want 1", one)
	}
	releaseFirst(ctx)
	select {
	case err := <-acquired:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("contender did not acquire after holder released")
	}
}
