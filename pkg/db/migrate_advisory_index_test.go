package db

// spec: §5

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

// A migration waiter must preserve exclusion without retaining a snapshot
// that prevents the current holder from building a concurrent index.
func TestAcquireMigrationLockWaiterAllowsConcurrentIndex(t *testing.T) {
	pool := pgtest.OpenDatabase(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	if _, err := pool.Exec(ctx, "CREATE TABLE migration_index_probe(id bigint); INSERT INTO migration_index_probe VALUES(1)"); err != nil {
		t.Fatal(err)
	}
	cfg := pool.Config()
	if cfg.ConnConfig.RuntimeParams == nil {
		cfg.ConnConfig.RuntimeParams = map[string]string{}
	}
	cfg.ConnConfig.RuntimeParams["application_name"] = "migration-index-waiter"
	cfg.MaxConns = 1
	waiterPool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(waiterPool.Close)
	first, err := AcquireMigrationLock(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = first(context.Background()) })
	done := make(chan error, 1)
	go func() {
		release, err := AcquireMigrationLock(ctx, waiterPool)
		if err == nil {
			err = release(context.Background())
		}
		done <- err
	}()
	for {
		var attempted bool
		err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity
			WHERE datname=current_database() AND application_name='migration-index-waiter'
			AND query LIKE '%advisory_lock%')`).Scan(&attempted)
		if err != nil {
			t.Fatal(err)
		}
		if attempted {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("waiter did not attempt the migration lock", ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
	indexCtx, cancelIndex := context.WithTimeout(ctx, 5*time.Second)
	defer cancelIndex()
	if _, err := pool.Exec(indexCtx, "CREATE INDEX CONCURRENTLY migration_index_probe_idx ON migration_index_probe(id)"); err != nil {
		t.Fatal("migration lock waiter blocked concurrent index", err)
	}
	select {
	case err := <-done:
		t.Fatal("waiter passed the migration gate before release", err)
	default:
	}
	if err := first(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal("waiter failed after holder released", err)
		}
	case <-ctx.Done():
		t.Fatal("waiter did not acquire the released lock", ctx.Err())
	}
}

func TestAcquireMigrationLockCancelledWaitReleasesConnection(t *testing.T) {
	pool := pgtest.OpenDatabase(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	first, err := AcquireMigrationLock(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = first(context.Background()) })
	waitCtx, cancelWait := context.WithTimeout(ctx, 150*time.Millisecond)
	defer cancelWait()
	if _, err := AcquireMigrationLock(waitCtx, pool); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("cancelled waiter did not return its deadline", err)
	}
	if acquired := DirectPool(pool).Stat().AcquiredConns(); acquired != 1 {
		t.Fatalf("cancelled waiter retained a connection: %d acquired, want only the holder", acquired)
	}
	if err := first(ctx); err != nil {
		t.Fatal(err)
	}
	release, err := AcquireMigrationLock(ctx, pool)
	if err != nil {
		t.Fatal("lock unavailable after cancelling waiter", err)
	}
	if err := release(ctx); err != nil {
		t.Fatal(err)
	}
}
