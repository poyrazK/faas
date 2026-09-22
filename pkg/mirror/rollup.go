// Package mirror owns the hourly mirror summary and raw-ledger retention.
// Each invocation is marked counted in the same SQL statement that adds its
// contribution to the summary. Overlapping windows, retries, late commits and
// concurrent workers therefore cannot double-count an invocation (ADR-221).
package mirror

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// DefaultRollupInterval is the rollup and retention cadence.
const DefaultRollupInterval = 5 * time.Minute

// DefaultLedgerRetention preserves seven days of raw diagnostic results.
// Uncounted rows survive longer during outages, until their totals are safe.
const DefaultLedgerRetention = 7 * 24 * time.Hour

// RollupOnce adds previously uncounted results in the half-open [start, end)
// window. It returns the number of summary buckets touched. The generated SQL
// atomically claims ledger rows and adds their contributions; an error rolls
// back both, and a replay touches only results committed since the last pass.
func RollupOnce(ctx context.Context, db sqlc.DBTX, windowStart, windowEnd time.Time) (int64, error) {
	if !windowEnd.After(windowStart) {
		return 0, fmt.Errorf("mirror: rollup window end %s not after start %s", windowEnd, windowStart)
	}
	rows, err := sqlc.New().RollupMirrorResults(ctx, db, sqlc.RollupMirrorResultsParams{
		WindowStart: pgtype.Timestamptz{Time: windowStart, Valid: true},
		WindowEnd:   pgtype.Timestamptz{Time: windowEnd, Valid: true},
	})
	if err != nil {
		return 0, fmt.Errorf("mirror: rollup window [%s,%s): %w", windowStart, windowEnd, err)
	}
	return rows, nil
}

// SweepOldLedgerRows deletes only expired results already counted in a
// summary. A failed rollup or a late insert cannot lose uncounted history.
func SweepOldLedgerRows(ctx context.Context, db sqlc.DBTX, cutoff time.Time) (int64, error) {
	rows, err := sqlc.New().SweepCountedMirrorResults(ctx, db, pgtype.Timestamptz{Time: cutoff, Valid: true})
	if err != nil {
		return 0, fmt.Errorf("mirror: sweep rows older than %s: %w", cutoff, err)
	}
	return rows, nil
}

// RollupLoop drains the uncounted backlog on startup and every tick, then
// sweeps counted rows older than seven days. The pending-row partial index
// keeps replays cheap. There is no moving lower watermark: outages, delayed
// commits and dropped ticker events must not leave permanent holes.
func RollupLoop(ctx context.Context, db sqlc.DBTX, interval time.Duration, log *slog.Logger) {
	if interval <= 0 {
		interval = DefaultRollupInterval
	}
	if log == nil {
		log = slog.Default()
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		rollupTick(ctx, db, time.Now().UTC(), log)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func rollupTick(ctx context.Context, db sqlc.DBTX, end time.Time, log *slog.Logger) {
	if _, err := RollupOnce(ctx, db, time.Time{}, end); err != nil {
		log.Warn("mirror: summary rollup", "err", err)
		return
	}
	cutoff := end.Add(-DefaultLedgerRetention)
	if _, err := SweepOldLedgerRows(ctx, db, cutoff); err != nil {
		log.Warn("mirror: ledger sweep", "cutoff", cutoff.Format(time.RFC3339), "err", err)
	}
}
