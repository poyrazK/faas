package db

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

// TestConnectionWarmupsRunOnMigratedSchema runs every warm-up against the
// migrated schema. A warm-up swallows its own errors in production, so a
// renamed or dropped table would only leave connections cold; fail here instead.
func TestConnectionWarmupsRunOnMigratedSchema(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	ctx := context.Background()
	if err := MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	for daemon, statement := range DaemonConnectionWarmups {
		t.Run(daemon, func(t *testing.T) {
			if _, err := pool.Exec(ctx, statement); err != nil {
				t.Fatalf("warm-up statement: %v", err)
			}
			hook := warmConnection("faas-" + daemon)
			if hook == nil {
				t.Fatal("no warm-up hook")
			}
			conn, err := pool.Acquire(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Release()
			if err := hook(ctx, conn.Conn()); err != nil {
				t.Fatalf("warm-up hook: %v", err)
			}
		})
	}
}

// TestConnectionWarmupIsBestEffort: a warm-up that fails on an open connection
// still admits it; one whose connection closed rejects it so the pool dials
// another.
func TestConnectionWarmupIsBestEffort(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	ctx := context.Background()
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	if err := runWarmup(`select 1 from warmup_missing_table limit 0`, connectionWarmupTimeout)(ctx, conn.Conn()); err != nil {
		t.Fatalf("failed warm-up rejected an open connection: %v", err)
	}
	var one int
	if err := conn.QueryRow(ctx, `select 1`).Scan(&one); err != nil || one != 1 {
		t.Fatalf("connection after a failed warm-up: %d %v", one, err)
	}
	closed, err := pgx.ConnectConfig(ctx, pool.Config().ConnConfig.Copy())
	if err != nil {
		t.Fatal(err)
	}
	if err := closed.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := runWarmup(`select 1`, connectionWarmupTimeout)(ctx, closed); err == nil {
		t.Fatal("a warm-up on a closed connection admitted it")
	}
}

// TestConnectionWarmupTimeoutKeepsConnection: a warm-up that outruns its bound
// fails on the server and leaves the session open with its own timeout, so a
// slow database does not make the pool dial again (2026-11-05 17:29).
func TestConnectionWarmupTimeoutKeepsConnection(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	ctx := context.Background()
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	var before string
	if err := conn.QueryRow(ctx, `show statement_timeout`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if err := runWarmup(`select pg_sleep(5)`, 100*time.Millisecond)(ctx, conn.Conn()); err != nil {
		t.Fatalf("a timed-out warm-up rejected an open connection: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("warm-up ran %s past its 100ms bound", elapsed)
	}
	if conn.Conn().IsClosed() {
		t.Fatal("the warm-up's timeout closed the connection")
	}
	var after string
	if err := conn.QueryRow(ctx, `show statement_timeout`).Scan(&after); err != nil || after != before {
		t.Fatalf("statement_timeout after the warm-up = %q (%v), want the session's %q", after, err, before)
	}
}
