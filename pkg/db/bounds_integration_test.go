package db_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/db"
)

func boundedTestPool(t *testing.T, size int32) *pgxpool.Pool {
	t.Helper()
	if os.Getenv("FAAS_SKIP_PG_TESTS") != "" || os.Getenv("DATABASE_URL") == "" {
		t.Skip("requires DATABASE_URL and enabled Postgres tests")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	pool, err := db.NewBoundedPool(ctx, os.Getenv("DATABASE_URL"), size)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	return pool
}

func TestBoundedPoolSaturationTimesOutAndRecovers(t *testing.T) {
	const size, callers = 2, 8
	pool := boundedTestPool(t, size)
	held := make([]*pgxpool.Conn, 0, size)
	for range size {
		conn, err := pool.Acquire(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		held = append(held, conn)
		defer conn.Release()
	}
	// Deliberately omit a deadline: cancellation is only an escape hatch if
	// the pool's fallback regresses. All query slots remain occupied.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	start, results := make(chan struct{}), make(chan error, callers)
	for range callers {
		go func() {
			<-start
			conn, err := pool.Acquire(ctx)
			if conn != nil {
				conn.Release()
			}
			results <- err
		}()
	}
	close(start)
	guard := time.NewTimer(db.AcquireTimeout + 5*time.Second)
	defer guard.Stop()
	for range callers {
		select {
		case err := <-results:
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Errorf("saturated acquisition = %v, want deadline exceeded", err)
			}
		case <-guard.C:
			cancel()
			t.Fatal("pool waiters outlived the fallback acquisition deadline")
		}
	}
	if stats := pool.Stat(); stats.TotalConns() != size || stats.AcquiredConns() != size || stats.CanceledAcquireCount() != callers {
		t.Fatalf("saturation: connections=%d acquired=%d canceled=%d; want %d/%d/%d",
			stats.TotalConns(), stats.AcquiredConns(), stats.CanceledAcquireCount(), size, size, callers)
	}
	for _, conn := range held {
		conn.Release()
	}
	ctx, cancelRecovery := context.WithTimeout(t.Context(), db.AcquireTimeout)
	defer cancelRecovery()
	if _, err := pool.Exec(ctx, "SELECT 1"); err != nil {
		t.Fatalf("pool did not recover after releasing its slots: %v", err)
	}
	if pool.Stat().AcquiredConns() != 0 {
		t.Fatal("pool retained a slot after the recovery query")
	}
}

func TestBoundedPoolQueryCancellationRecovers(t *testing.T) {
	pool := boundedTestPool(t, 1)
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	_, err := pool.Exec(ctx, "SELECT pg_sleep(60)")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("query ignored its earlier caller deadline: %v", err)
	}
	ctx, cancelRecovery := context.WithTimeout(t.Context(), db.AcquireTimeout)
	defer cancelRecovery()
	if _, err := pool.Exec(ctx, "SELECT 1"); err != nil {
		t.Fatalf("canceled query poisoned the pool: %v", err)
	}
}
