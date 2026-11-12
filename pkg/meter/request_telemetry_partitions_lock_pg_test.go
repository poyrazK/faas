//go:build !no_pg

// adr: 127
package meter

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

// holdTelemetryReader opens a transaction that reads request_telemetry and
// stays open, as a long reader or the retention sweep does. release ends it.
func holdTelemetryReader(t *testing.T, pool *pgxpool.Pool) (release func()) {
	t.Helper()
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(t.Context(), `select count(*) from request_telemetry`); err != nil {
		t.Fatal(err)
	}
	release = func() { _ = tx.Rollback(context.Background()) }
	t.Cleanup(release)
	return release
}

// A pass that queues for ACCESS EXCLUSIVE blocks every later reader of
// request_telemetry until the open transaction ahead of it ends. Prod
// 2026-11-09 21:17Z: the hourly pass queued behind the retention sweep and
// apid's telemetry reads waited 20 s. With every month attached the pass must
// not lock at all; with one missing it must give up at its lock timeout.
func TestEnsureRequestTelemetryPartitionsNeverQueuesBehindAReader(t *testing.T) {
	ctx := t.Context()
	pool := pgtest.Open(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	partDB := partitionTestDB{pool: pool}
	if _, err := EnsureRequestTelemetryPartitions(ctx, partDB); err != nil {
		t.Fatal(err)
	}

	release := holdTelemetryReader(t, pool)
	start := time.Now()
	coverage, err := EnsureRequestTelemetryPartitions(ctx, partDB)
	if err != nil || coverage.CoveredMonths != 3 {
		t.Fatalf("pass with every month attached = %+v, %v", coverage, err)
	}
	if waited := time.Since(start); waited > time.Second {
		t.Fatalf("pass with every month attached waited %s behind a reader; it must not lock", waited)
	}
	release()

	// Remove the last month, so the next pass needs the lock.
	var schemaName, lastName string
	if err := pool.QueryRow(ctx, `select current_schema(), 'request_telemetry_' || to_char(date_trunc('month', now()) + interval '2 months', 'YYYYMM')`).Scan(&schemaName, &lastName); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "DROP TABLE "+pgx.Identifier{schemaName, lastName}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	holdTelemetryReader(t, pool)
	start = time.Now()
	if _, err := EnsureRequestTelemetryPartitions(ctx, partDB); err == nil {
		t.Fatal("pass took ACCESS EXCLUSIVE past an open reader")
	}
	if waited := time.Since(start); waited > 4*time.Second {
		t.Fatalf("pass queued %s behind a reader; it must give up at its lock timeout", waited)
	}
}
