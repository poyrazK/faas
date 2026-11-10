package state_test

// adr: 570

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

// A policy read that outlives its caller's deadline used to cost the pool its
// connection: pgx closed it, and the next read dialed TLS, SCRAM and a new
// backend into the slow database (gatewayd-internal, 2026-11-10 12:01/12:17).
func TestServicePolicySnapshotPastItsDeadlineKeepsTheConnection(t *testing.T) {
	shared := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(t.Context(), shared); err != nil {
		t.Fatal(err)
	}
	cfg := shared.Config().Copy()
	cfg.MaxConns, cfg.MinConns = 1, 0
	pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	s := state.NewPgStore(pool)
	pid := backendPID(t.Context(), t, pool)

	// Hold apps so the snapshot's read waits on the lock past its deadline.
	blocker, err := shared.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = blocker.Rollback(context.Background()) })
	if _, err := blocker.Exec(t.Context(), `lock table apps in access exclusive mode`); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), api.TrafficPublicHostReadTimeout)
	defer cancel()
	started := time.Now()
	err = s.WithServicePolicySnapshot(ctx, func(r state.ServicePolicyReader) error {
		_, err := r.AppByID(ctx, uuid.NewString())
		return err
	})
	elapsed := time.Since(started)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("snapshot past its deadline returned %v, want context.DeadlineExceeded", err)
	}
	if limit := api.TrafficPublicHostReadTimeout + api.TrafficPolicySnapshotCancelGrace + 500*time.Millisecond; elapsed > limit {
		t.Fatalf("snapshot returned after %s, want within %s", elapsed, limit)
	}

	// The cancelled session rolls back off the caller's path and returns to
	// the pool; the lock is still held, so only a cancel could have freed it.
	waitCtx, waitCancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer waitCancel()
	if got := backendPID(waitCtx, t, pool); got != pid {
		t.Fatalf("pool dialed backend %d after the deadline, want the kept backend %d", got, pid)
	}
	if n := pool.Stat().NewConnsCount(); n != 1 {
		t.Fatalf("pool opened %d connections, want 1", n)
	}

	// The kept session reads normally once the database answers again.
	if err := blocker.Rollback(t.Context()); err != nil {
		t.Fatal(err)
	}
	err = s.WithServicePolicySnapshot(t.Context(), func(r state.ServicePolicyReader) error {
		_, err := r.AppByID(t.Context(), uuid.NewString())
		return err
	})
	if !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("read on the kept session returned %v, want ErrNotFound", err)
	}
}

// backendPID reads the backend of pool's one connection, waiting until ctx
// ends for it to return to the pool.
func backendPID(ctx context.Context, t *testing.T, pool *pgxpool.Pool) int32 {
	t.Helper()
	var pid int32
	if err := pool.QueryRow(ctx, `select pg_backend_pid()`).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	return pid
}
