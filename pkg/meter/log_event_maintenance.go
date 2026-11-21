package meter

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
)

// ADR-211's maximum customer log retention is Scale's 90-day archive cap.
// The shorter Free, Hobby, and Pro windows are enforced by the row sweep.
const MaxLogEventRetentionDays = 90

const LogEventMaintenanceInterval = time.Hour

// Partition attachment holds an ACCESS EXCLUSIVE lock while relocating rows
// from the default partition. The lock timeout bounds acquisition only; this
// deadline also bounds the work after the lock has been acquired. On timeout,
// PostgreSQL rolls back the move and the default partition keeps accepting
// events until the next pass or an operator drains a large backlog.
const LogEventPartitionReconcileTimeout = 5 * time.Second

// A coarse one-day bound lets log_events_retention_time_idx find the oldest
// candidates before the billing-plan predicate is evaluated. The primary-key
// tuple identifies a row across partitions; ctid alone is not unique there.
const retentionLogEventsBatchSQL = `
WITH expired AS MATERIALIZED (
    SELECT e.id, e.occurred_at
      FROM log_events e
      LEFT JOIN app_resource_accounts ac ON ac.app_id = e.app_id AND ac.id = e.account_id
      LEFT JOIN accounts legacy ON legacy.id = e.account_id
     WHERE e.occurred_at < now() - interval '1 day'
       AND e.occurred_at < now() - (CASE coalesce(ac.plan, legacy.plan)
           WHEN 'free'  THEN interval '1 day'
           WHEN 'hobby' THEN interval '7 days'
           WHEN 'pro'   THEN interval '30 days'
           WHEN 'scale' THEN interval '90 days'
           ELSE              interval '1 day'
       END)
     ORDER BY e.occurred_at, e.id
     LIMIT $1
)
DELETE FROM log_events e
 USING expired x
 WHERE e.id = x.id AND e.occurred_at = x.occurred_at`

// RetentionOnceLogEvents removes expired projections in bounded batches. A
// missing billing policy uses the one-day floor, so orphan records cannot persist
// indefinitely. The loop's cap makes a large backlog visible to meterd.
func RetentionOnceLogEvents(ctx context.Context, db retentionExecer) (int64, error) {
	var total int64
	for i := 0; i < MaxRetentionBatches; i++ {
		deleted, err := db.Exec(ctx, retentionLogEventsBatchSQL, RetentionBatchSize)
		if err != nil {
			return total, fmt.Errorf("log event retention delete (batch %d, deleted so far %d): %w", i, total, err)
		}
		total += deleted
		if deleted < RetentionBatchSize {
			return total, nil
		}
	}
	return total, ErrRetentionBatchCap
}

type logEventMaintenanceDB interface {
	retentionExecer
	QueryRow(context.Context, string, ...any) pgx.Row
}

// LogEventPartitionCoverage reports whether writes have explicit partitions
// ahead of them and whether the default catch-all is accumulating events.
type LogEventPartitionCoverage struct {
	CoveredMonths       int64
	CurrentMonthCovered bool
	DefaultRows         int64
	DefaultLatest       time.Time
}

// ensureLogEventPartitionsSQL keeps the current and next two UTC months
// attached, moving overlapping default rows first, as the schema owner in
// monthly_partitions_ensure (migration 20261118162630014).
const ensureLogEventPartitionsSQL = `SELECT monthly_partitions_ensure('log_events'::regclass)`

const logEventPartitionCoverageSQL = `
WITH expected(name, ordinal) AS (
    SELECT 'log_events_' || to_char(date_trunc('month', now() AT TIME ZONE 'UTC') + make_interval(months => n), 'YYYYMM'), n
      FROM generate_series(0, 2) AS n
), attached AS (
    SELECT child.relname FROM pg_inherits i
    JOIN pg_class child ON child.oid = i.inhrelid
    WHERE i.inhparent = format('%I.log_events', current_schema())::regclass
      AND child.relnamespace = current_schema()::regnamespace
)
SELECT count(attached.relname),
       COALESCE(bool_or(expected.ordinal = 0 AND attached.relname IS NOT NULL), false),
       (SELECT count(*) FROM log_events_default),
       COALESCE((SELECT max(occurred_at) FROM log_events_default), 'epoch'::timestamptz)
  FROM expected LEFT JOIN attached ON attached.relname = expected.name`

// EnsureLogEventPartitions keeps the current and next two UTC months attached.
// Replays or forward-dated events that landed in the default partition are
// moved before an overlapping explicit partition is attached.
func EnsureLogEventPartitions(ctx context.Context, db logEventMaintenanceDB) (LogEventPartitionCoverage, error) {
	reconcileCtx, cancel := context.WithTimeout(ctx, LogEventPartitionReconcileTimeout)
	defer cancel()
	if _, err := db.Exec(reconcileCtx, ensureLogEventPartitionsSQL); err != nil {
		return LogEventPartitionCoverage{}, fmt.Errorf("ensure log event partitions: %w", err)
	}
	var coverage LogEventPartitionCoverage
	if err := db.QueryRow(ctx, logEventPartitionCoverageSQL).Scan(
		&coverage.CoveredMonths, &coverage.CurrentMonthCovered,
		&coverage.DefaultRows, &coverage.DefaultLatest,
	); err != nil {
		return LogEventPartitionCoverage{}, fmt.Errorf("inspect log event partitions: %w", err)
	}
	return coverage, nil
}

// dropExpiredLogEventPartitionsSQL drops a whole partition only after its UTC
// upper bound has passed the longest plan window (90 days), as the schema owner
// in monthly_partitions_drop_expired (migration 20261118162630014).
const dropExpiredLogEventPartitionsSQL = `SELECT monthly_partitions_drop_expired('log_events'::regclass)`

func DropExpiredLogEventPartitions(ctx context.Context, db retentionExecer) error {
	if _, err := db.Exec(ctx, dropExpiredLogEventPartitionsSQL); err != nil {
		return fmt.Errorf("drop expired log event partitions: %w", err)
	}
	return nil
}

// LogEventPartitionLoop reconciles at boot and hourly. The callback owns
// operator metrics; the package itself has no Prometheus dependency.
func LogEventPartitionLoop(ctx context.Context, db logEventMaintenanceDB, interval time.Duration, log *slog.Logger, observe func(LogEventPartitionCoverage, error)) {
	if interval <= 0 {
		interval = LogEventMaintenanceInterval
	}
	run := func() {
		coverage, err := EnsureLogEventPartitions(ctx, db)
		if observe != nil {
			observe(coverage, err)
		}
		if err != nil && log != nil {
			log.Error("log event partition reconciliation failed", "err", err)
		}
	}
	run()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run()
		}
	}
}

// LogEventRetentionLoop sweeps at boot and hourly, then drops whole partitions
// past the 90-day maximum. Failures remain visible and retry on the next tick.
func LogEventRetentionLoop(ctx context.Context, db retentionExecer, interval time.Duration, log *slog.Logger, observe func(int64, error)) {
	if interval <= 0 {
		interval = LogEventMaintenanceInterval
	}
	run := func() {
		deleted, sweepErr := RetentionOnceLogEvents(ctx, db)
		dropErr := DropExpiredLogEventPartitions(ctx, db)
		if observe != nil {
			observe(deleted, errors.Join(sweepErr, dropErr))
		}
		if log != nil {
			if sweepErr == nil {
				log.Info("log event retention tick ok", "rows_deleted", deleted)
			} else if errors.Is(sweepErr, ErrRetentionBatchCap) {
				log.Warn("log event retention hit batch cap", "rows_deleted", deleted, "err", sweepErr)
			} else {
				log.Error("log event retention failed", "rows_deleted", deleted, "err", sweepErr)
			}
			if dropErr != nil {
				log.Warn("log event partition drop failed", "err", dropErr)
			}
		}
	}
	run()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run()
		}
	}
}
