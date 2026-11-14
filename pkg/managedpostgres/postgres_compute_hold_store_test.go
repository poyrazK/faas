//go:build !no_pg

package managedpostgres

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestPostgresComputeHoldLease(t *testing.T) {
	testComputeHoldLease(t, func(t *testing.T) computeHoldFixture {
		store, _, _, account := postgresStoreFixture(t)
		return computeHoldFixture{store, store, postgresResizeDatabase(t, store, account), time.Now().UTC()}
	})
}

func TestPostgresComputeHoldLeaseClockAfterLockWait(t *testing.T) {
	store, pool, ctx, account := postgresStoreFixture(t)
	d := postgresResizeDatabase(t, store, account)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err := tx.Exec(ctx, "SELECT id FROM managed_postgres_databases WHERE id=$1 FOR UPDATE", d.ID); err != nil {
		t.Fatal(err)
	}
	done := make(chan Database, 1)
	errs := make(chan error, 1)
	go func() {
		now := time.Now()
		claimed, err := store.ClaimComputeHold(ctx, d, "waiting", now, now.Add(300*time.Millisecond))
		done <- claimed
		errs <- err
	}()
	time.Sleep(500 * time.Millisecond)
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	claimed := <-done
	if err := <-errs; err != nil {
		t.Fatal(err)
	}
	if !claimed.LeaseUntil.After(time.Now()) {
		t.Fatal("claim lease expired while waiting for the row lock")
	}
	// A release must check expiry after waiting, too.
	tx, err = pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err = tx.Exec(ctx, "SELECT id FROM managed_postgres_databases WHERE id=$1 FOR UPDATE", d.ID); err != nil {
		t.Fatal(err)
	}
	go func() { errs <- store.ReleaseComputeHold(ctx, d.ID, "waiting", time.Now()) }()
	time.Sleep(time.Until(claimed.LeaseUntil) + 30*time.Millisecond)
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-errs; !errors.Is(err, ErrConflict) {
		t.Fatal("release accepted an expired lease after waiting", err)
	}
}
