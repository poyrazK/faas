package meter

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
)

// RequestTelemetryPartitionInterval keeps three explicit monthly partitions
// ahead of writes. The loop also runs immediately at daemon boot.
const RequestTelemetryPartitionInterval = time.Hour

type requestTelemetryPartitionDB interface {
	retentionExecer
	QueryRow(context.Context, string, ...any) pgx.Row
}

// RequestTelemetryPartitionCoverage is the bounded operator view returned
// after each reconciliation pass.
type RequestTelemetryPartitionCoverage struct {
	CoveredMonths         int64
	CurrentMonthCovered   bool
	DefaultRows           int64
	DefaultLatestReceived time.Time
}

// RequestTelemetryPartitionReconcileTimeout bounds one reconciliation pass,
// the work after the lock as well as acquiring it (as for log_events).
const RequestTelemetryPartitionReconcileTimeout = 5 * time.Second

// ensureRequestTelemetryPartitionsSQL keeps the current and next two months
// attached, moving overlapping default rows first. The work runs as the schema
// owner inside monthly_partitions_ensure (migration 20261118162630014): meterd's
// runtime login holds no DDL. The function returns before locking when the
// months are attached, and its lock timeout makes a busy pass retry next hour
// instead of queueing ahead of every reader (prod 2026-11-09 21:17Z).
const ensureRequestTelemetryPartitionsSQL = `SELECT monthly_partitions_ensure('request_telemetry'::regclass)`

const requestTelemetryPartitionCoverageSQL = `
WITH expected(name, ordinal) AS (
    SELECT 'request_telemetry_' || to_char(date_trunc('month', now()) + make_interval(months => n), 'YYYYMM'), n
      FROM generate_series(0, 2) AS n
), attached AS (
    SELECT child.relname
      FROM pg_inherits i
      JOIN pg_class child ON child.oid = i.inhrelid
     WHERE i.inhparent = format('%I.request_telemetry', current_schema())::regclass
       AND child.relnamespace = current_schema()::regnamespace
)
SELECT count(attached.relname),
       bool_or(expected.ordinal = 0 AND attached.relname IS NOT NULL),
       (SELECT count(*) FROM request_telemetry_default),
       COALESCE((SELECT max(received_at) FROM request_telemetry_default), 'epoch'::timestamptz)
  FROM expected
  LEFT JOIN attached ON attached.relname = expected.name`

func EnsureRequestTelemetryPartitions(ctx context.Context, db requestTelemetryPartitionDB) (RequestTelemetryPartitionCoverage, error) {
	reconcileCtx, cancel := context.WithTimeout(ctx, RequestTelemetryPartitionReconcileTimeout)
	defer cancel()
	if _, err := db.Exec(reconcileCtx, ensureRequestTelemetryPartitionsSQL); err != nil {
		return RequestTelemetryPartitionCoverage{}, fmt.Errorf("ensure request telemetry partitions: %w", err)
	}
	var coverage RequestTelemetryPartitionCoverage
	if err := db.QueryRow(ctx, requestTelemetryPartitionCoverageSQL).Scan(
		&coverage.CoveredMonths,
		&coverage.CurrentMonthCovered,
		&coverage.DefaultRows,
		&coverage.DefaultLatestReceived,
	); err != nil {
		return RequestTelemetryPartitionCoverage{}, fmt.Errorf("inspect request telemetry partitions: %w", err)
	}
	return coverage, nil
}

// RequestTelemetryPartitionLoop reconciles immediately and then hourly. The
// callback owns metrics so this leaf package stays independent of Prometheus.
func RequestTelemetryPartitionLoop(ctx context.Context, db requestTelemetryPartitionDB, interval time.Duration, log *slog.Logger, observe func(RequestTelemetryPartitionCoverage, error)) {
	if interval <= 0 {
		interval = RequestTelemetryPartitionInterval
	}
	run := func() {
		coverage, err := EnsureRequestTelemetryPartitions(ctx, db)
		if observe != nil {
			observe(coverage, err)
		}
		if err != nil && log != nil {
			log.Error("request telemetry partition reconciliation failed", "err", err)
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
