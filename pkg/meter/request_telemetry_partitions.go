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

// ensureRequestTelemetryPartitionsSQL moves any overlapping default rows into
// a standalone table before attaching it. ACCESS EXCLUSIVE serializes the
// short DDL transaction with inserts, closing the gap between DELETE and
// ATTACH. All statements roll back together on failure.
const ensureRequestTelemetryPartitionsSQL = `
DO $$
DECLARE
    month_offset integer;
    range_start timestamptz;
    range_end timestamptz;
    partition_name text;
    schema_name text := current_schema();
    is_attached boolean;
BEGIN
    EXECUTE format(
        'LOCK TABLE %I.request_telemetry IN ACCESS EXCLUSIVE MODE',
        schema_name);
    FOR month_offset IN 0..2 LOOP
        range_start := date_trunc('month', now()) + make_interval(months => month_offset);
        range_end := range_start + interval '1 month';
        partition_name := 'request_telemetry_' || to_char(range_start, 'YYYYMM');

        SELECT EXISTS (
            SELECT 1
              FROM pg_inherits i
              JOIN pg_class child ON child.oid = i.inhrelid
              JOIN pg_class parent ON parent.oid = i.inhparent
              JOIN pg_namespace ns ON ns.oid = child.relnamespace
             WHERE parent.oid = format('%I.request_telemetry', schema_name)::regclass
               AND ns.nspname = schema_name
               AND child.relname = partition_name
        ) INTO is_attached;
        IF is_attached THEN
            CONTINUE;
        END IF;
        IF to_regclass(format('%I.%I', schema_name, partition_name)) IS NOT NULL THEN
            RAISE EXCEPTION 'request telemetry partition % exists but is not attached', partition_name;
        END IF;

        EXECUTE format(
            'CREATE TABLE %I.%I (LIKE %I.request_telemetry INCLUDING ALL)',
            schema_name, partition_name, schema_name);
        EXECUTE format(
            'INSERT INTO %I.%I SELECT * FROM %I.request_telemetry_default WHERE received_at >= %L AND received_at < %L',
            schema_name, partition_name, schema_name, range_start, range_end);
        EXECUTE format(
            'DELETE FROM %I.request_telemetry_default WHERE received_at >= %L AND received_at < %L',
            schema_name, range_start, range_end);
        EXECUTE format(
            'ALTER TABLE %I.request_telemetry ATTACH PARTITION %I.%I FOR VALUES FROM (%L) TO (%L)',
            schema_name, schema_name, partition_name, range_start, range_end);
    END LOOP;
END $$;`

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
	if _, err := db.Exec(ctx, ensureRequestTelemetryPartitionsSQL); err != nil {
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
