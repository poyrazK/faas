//go:build !no_pg

// adr: 133

package mirror

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func mirrorDB(t *testing.T) (*pgxpool.Pool, uuid.UUID) {
	t.Helper()
	pool, rule := legacyMirrorDB(t)
	applyMirrorMigration(t, pool, "20260922164807594_mirror_rollup_counted.sql")
	return pool, rule
}

func legacyMirrorDB(t *testing.T) (*pgxpool.Pool, uuid.UUID) {
	t.Helper()
	pool := pgtest.Open(t)
	// Only the rule's identity and app are used by the rollup. Apply the real
	// ledger and summary DDL so these tests don't need the unrelated VM schema.
	if _, err := pool.Exec(t.Context(), `CREATE TABLE mirror_rules (id uuid PRIMARY KEY, app_id uuid NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	applyMirrorMigration(t, pool, "00386_mirror_invocation_results.sql")
	applyMirrorMigration(t, pool, "00515_mirror_invocation_summary.sql")
	rule := uuid.New()
	if _, err := pool.Exec(t.Context(), `INSERT INTO mirror_rules VALUES ($1, $2)`, rule, uuid.New()); err != nil {
		t.Fatal(err)
	}
	return pool, rule
}

func applyMirrorMigration(t *testing.T, pool *pgxpool.Pool, name string) {
	t.Helper()
	data, err := migrations.FS.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	up, _, _ := strings.Cut(string(data), "-- +goose Down")
	if _, err := pool.Exec(t.Context(), up); err != nil {
		t.Fatalf("apply %s: %v", name, err)
	}
}

func insertMirrorResult(t *testing.T, pool *pgxpool.Pool, rule uuid.UUID, completed time.Time) {
	t.Helper()
	_, err := pool.Exec(t.Context(), `INSERT INTO mirror_invocation_results (
		mirror_rule_id, app_id, account_id, source_deployment_id, mirror_deployment_id,
		request_id, completed_at, latency_ms, status_diff, schema_diff, body_diff, crashed
	) SELECT id, app_id, $2, $3, $4, $5, $6, 37, true, true, true, true
	FROM mirror_rules WHERE id = $1`, rule, uuid.New(), uuid.New(), uuid.New(), uuid.NewString(), completed)
	if err != nil {
		t.Fatal(err)
	}
}

func assertMirrorSummary(t *testing.T, pool *pgxpool.Pool, rule uuid.UUID, hour time.Time, want int64) {
	t.Helper()
	var bucket time.Time
	var total, status, schema, body, crash, latency int64
	err := pool.QueryRow(t.Context(), `SELECT hour_bucket, total_invocations,
		status_diff_count, schema_diff_count, body_diff_count, crash_count, sum_latency_ms
		FROM mirror_invocation_summary WHERE rule_id = $1`, rule).
		Scan(&bucket, &total, &status, &schema, &body, &crash, &latency)
	if err != nil {
		t.Fatal(err)
	}
	if !bucket.Equal(hour) || total != want || status != want || schema != want || body != want || crash != want || latency != 37*want {
		t.Fatalf("summary = (%s, %d, %d, %d, %d, %d, %d), want (%s, %d each, latency %d)",
			bucket, total, status, schema, body, crash, latency, hour, want, 37*want)
	}
}

func TestRollupReplayAndOverlappingWindowsPG(t *testing.T) {
	pool, rule := mirrorDB(t)
	hour := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	insertMirrorResult(t, pool, rule, hour.Add(time.Minute))
	for i := 0; i < 2; i++ {
		if _, err := RollupOnce(t.Context(), pool, hour, hour.Add(5*time.Minute)); err != nil {
			t.Fatal(err)
		}
		assertMirrorSummary(t, pool, rule, hour, 1)
	}
	// A late commit inside the replayed interval and a new partial-hour row
	// must both be counted, without adding the original invocation again.
	insertMirrorResult(t, pool, rule, hour.Add(2*time.Minute))
	insertMirrorResult(t, pool, rule, hour.Add(8*time.Minute))
	if _, err := RollupOnce(t.Context(), pool, hour, hour.Add(10*time.Minute)); err != nil {
		t.Fatal(err)
	}
	assertMirrorSummary(t, pool, rule, hour, 3)
}

func TestRollupHalfOpenWindowPG(t *testing.T) {
	pool, rule := mirrorDB(t)
	hour := time.Now().UTC().Truncate(time.Hour)
	for _, minute := range []int{9, 10, 20} {
		insertMirrorResult(t, pool, rule, hour.Add(time.Duration(minute)*time.Minute))
	}
	if _, err := RollupOnce(t.Context(), pool, hour.Add(10*time.Minute), hour.Add(20*time.Minute)); err != nil {
		t.Fatal(err)
	}
	assertMirrorSummary(t, pool, rule, hour, 1)
	if _, err := RollupOnce(t.Context(), pool, hour, hour.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	assertMirrorSummary(t, pool, rule, hour, 3)
}

func TestRollupHourIsUTCRegardlessOfSessionPG(t *testing.T) {
	pool, rule := mirrorDB(t)
	hour := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	insertMirrorResult(t, pool, rule, hour.Add(45*time.Minute))
	// One pooled connection is enough for this sequential test. A half-hour
	// offset exposes date_trunc(timestamptz)'s dependence on session timezone.
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err := tx.Exec(t.Context(), `SET LOCAL TIME ZONE 'Asia/Kolkata'`); err != nil {
		t.Fatal(err)
	}
	if _, err := RollupOnce(t.Context(), tx, hour, hour.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	assertMirrorSummary(t, pool, rule, hour, 1)
}

func TestSweepPreservesUncountedResultsPG(t *testing.T) {
	pool, rule := mirrorDB(t)
	hour := time.Now().UTC().Add(-8 * 24 * time.Hour).Truncate(time.Hour)
	insertMirrorResult(t, pool, rule, hour)
	cutoff := time.Now().UTC().Add(-DefaultLedgerRetention)
	if deleted, err := SweepOldLedgerRows(t.Context(), pool, cutoff); err != nil || deleted != 0 {
		t.Fatalf("sweep before rollup = %d, %v; want 0, nil", deleted, err)
	}
	if _, err := RollupOnce(t.Context(), pool, hour, hour.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if deleted, err := SweepOldLedgerRows(t.Context(), pool, cutoff); err != nil || deleted != 1 {
		t.Fatalf("sweep after rollup = %d, %v; want 1, nil", deleted, err)
	}
	// A late old result must add to, not replace, the archived hour.
	insertMirrorResult(t, pool, rule, hour.Add(time.Minute))
	if _, err := RollupOnce(t.Context(), pool, hour, hour.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	assertMirrorSummary(t, pool, rule, hour, 2)
}

func TestRollupConcurrentWorkersPG(t *testing.T) {
	pool, rule := mirrorDB(t)
	hour := time.Now().UTC().Truncate(time.Hour)
	for i := 0; i < 40; i++ {
		insertMirrorResult(t, pool, rule, hour.Add(time.Duration(i)*time.Second))
	}
	var workers sync.WaitGroup
	start := make(chan struct{})
	errs := make(chan error, 6)
	for i := 0; i < cap(errs); i++ {
		workers.Go(func() {
			<-start
			_, err := RollupOnce(t.Context(), pool, hour, hour.Add(time.Hour))
			errs <- err
		})
	}
	close(start)
	workers.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	assertMirrorSummary(t, pool, rule, hour, 40)
}

func TestRollupFailureRollsBackReceiptsAndRecoversOldBacklogPG(t *testing.T) {
	pool, rule := mirrorDB(t)
	hour := time.Now().UTC().Add(-9 * 24 * time.Hour).Truncate(time.Hour)
	for i := 0; i < 2; i++ {
		insertMirrorResult(t, pool, rule, hour.Add(time.Duration(i)*time.Minute))
	}
	if _, err := pool.Exec(t.Context(), `ALTER TABLE mirror_invocation_summary
		ADD CONSTRAINT reject_rollup CHECK (total_invocations < 2)`); err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	rollupTick(t.Context(), pool, time.Now().UTC(), log)
	var pending int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM mirror_invocation_results
		WHERE NOT rollup_counted`).Scan(&pending); err != nil || pending != 2 {
		t.Fatalf("pending after failed rollup = %d, err=%v", pending, err)
	}
	if _, err := pool.Exec(t.Context(), `ALTER TABLE mirror_invocation_summary DROP CONSTRAINT reject_rollup`); err != nil {
		t.Fatal(err)
	}
	rollupTick(t.Context(), pool, time.Now().UTC(), log)
	assertMirrorSummary(t, pool, rule, hour, 2)
	var remaining int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM mirror_invocation_results`).Scan(&remaining); err != nil || remaining != 0 {
		t.Fatalf("remaining after recovery = %d, err=%v", remaining, err)
	}
}

func TestRollupPicksUpLateCommitPG(t *testing.T) {
	pool, rule := mirrorDB(t)
	hour := time.Now().UTC().Add(-4 * 24 * time.Hour).Truncate(time.Hour)
	insertMirrorResult(t, pool, rule, hour)
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err := tx.Exec(t.Context(), `UPDATE mirror_invocation_results SET latency_ms = 37`); err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	rollupTick(t.Context(), pool, time.Now().UTC(), log) // Locked row skipped.
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	rollupTick(t.Context(), pool, time.Now().UTC(), log)
	assertMirrorSummary(t, pool, rule, hour, 1)
}

func TestRollupMigrationBaselineAndReplayPG(t *testing.T) {
	for _, age := range []time.Duration{time.Hour, 8 * 24 * time.Hour} {
		t.Run(age.String(), func(t *testing.T) {
			pool, rule := legacyMirrorDB(t)
			hour := time.Now().UTC().Add(-age).Truncate(time.Hour)
			insertMirrorResult(t, pool, rule, hour)
			// Existing recent totals can be repaired. Older totals may include
			// already-pruned raw rows and must not shrink to the retained count.
			if _, err := pool.Exec(t.Context(), `INSERT INTO mirror_invocation_summary
				(rule_id, app_id, hour_bucket, total_invocations, status_diff_count,
				 schema_diff_count, body_diff_count, crash_count, sum_latency_ms)
				SELECT id, app_id, $2, 9, 9, 9, 9, 9, 333 FROM mirror_rules WHERE id = $1`, rule, hour); err != nil {
				t.Fatal(err)
			}
			applyMirrorMigration(t, pool, "20260922164807594_mirror_rollup_counted.sql")
			want := int64(1)
			if age > DefaultLedgerRetention {
				want = 9
			}
			assertMirrorSummary(t, pool, rule, hour, want)
			if _, err := RollupOnce(t.Context(), pool, hour, hour.Add(time.Hour)); err != nil {
				t.Fatal(err)
			}
			assertMirrorSummary(t, pool, rule, hour, want)
			// Prune the baseline, add a late result, then replay the migration.
			// Re-baselining now would destroy history or suppress this new row.
			if _, err := SweepOldLedgerRows(t.Context(), pool, hour.Add(time.Hour)); err != nil {
				t.Fatal(err)
			}
			insertMirrorResult(t, pool, rule, hour.Add(time.Minute))
			applyMirrorMigration(t, pool, "20260922164807594_mirror_rollup_counted.sql")
			if _, err := RollupOnce(t.Context(), pool, hour, hour.Add(time.Hour)); err != nil {
				t.Fatal(err)
			}
			assertMirrorSummary(t, pool, rule, hour, want+1)
		})
	}
}

func TestRollupMigrationRepairsLegacyTimezoneBucketPG(t *testing.T) {
	pool, rule := legacyMirrorDB(t)
	hour := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Hour)
	insertMirrorResult(t, pool, rule, hour.Add(45*time.Minute))
	if _, err := pool.Exec(t.Context(), `INSERT INTO mirror_invocation_summary
		(rule_id, app_id, hour_bucket, total_invocations)
		SELECT id, app_id, $2, 9 FROM mirror_rules WHERE id = $1`, rule, hour.Add(30*time.Minute)); err != nil {
		t.Fatal(err)
	}
	applyMirrorMigration(t, pool, "20260922164807594_mirror_rollup_counted.sql")
	assertMirrorSummary(t, pool, rule, hour, 1)
}
