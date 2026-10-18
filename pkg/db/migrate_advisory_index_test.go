package db

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

// A migrator waiting for the fleet lock must not keep a snapshot that prevents
// the lock holder's nontransactional concurrent index builds from completing.
func TestAcquireMigrationLock_ContendersAllowConcurrentIndex(t *testing.T) {
	pool := pgtest.OpenDatabase(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	if _, err := pool.Exec(ctx, `CREATE TABLE migration_index_probe (id integer)`); err != nil {
		t.Fatal(err)
	}
	release, err := AcquireMigrationLock(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	waitCtx, stop := context.WithCancel(ctx)
	const contenders = 2
	done := make(chan error, contenders)
	var wg sync.WaitGroup
	defer func() {
		stop()
		_ = release(context.WithoutCancel(ctx))
		wg.Wait()
	}()
	for range contenders {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := AcquireMigrationLock(waitCtx, pool)
			if err == nil {
				err = r(context.WithoutCancel(waitCtx))
			}
			done <- err
		}()
	}

	// Wait until all pinned sessions have executed a query. The observer uses a
	// fourth connection and excludes itself, so this works for both blocking
	// acquisition and acquisition that leaves the waiting sessions idle.
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		var sessions int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity
WHERE datname=current_database() AND pid<>pg_backend_pid() AND query_start IS NOT NULL`).Scan(&sessions); err != nil {
			t.Fatal(err)
		}
		if pool.Stat().AcquiredConns() >= contenders+1 && sessions >= contenders+1 {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-tick.C:
		}
	}
	indexCtx, cancelIndex := context.WithTimeout(ctx, 5*time.Second)
	defer cancelIndex()
	if _, err := pool.Exec(indexCtx, `CREATE INDEX CONCURRENTLY migration_index_probe_idx ON migration_index_probe (id)`); err != nil {
		t.Fatalf("migration-lock contenders blocked the concurrent index: %v", err)
	}
	if err := release(ctx); err != nil {
		t.Fatal(err)
	}
	for range contenders {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
}

func TestAcquireMigrationLock_CancelledReleaseDoesNotStrandLock(t *testing.T) {
	pool := pgtest.OpenDatabase(t)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	release, err := AcquireMigrationLock(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	cancelled, stop := context.WithCancel(ctx)
	stop()
	if err := release(cancelled); err == nil {
		t.Fatal("unlock with a cancelled context unexpectedly succeeded")
	}

	// A different pool guarantees the next attempt uses another PostgreSQL
	// session rather than reentering a leaked advisory lock on the old session.
	other, err := pgxpool.NewWithConfig(ctx, pool.Config())
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	acquireCtx, cancelAcquire := context.WithTimeout(ctx, 5*time.Second)
	defer cancelAcquire()
	next, err := AcquireMigrationLock(acquireCtx, other)
	if err != nil {
		t.Fatalf("cancelled release stranded the migration lock: %v", err)
	}
	if err := next(ctx); err != nil {
		t.Fatal(err)
	}
}
