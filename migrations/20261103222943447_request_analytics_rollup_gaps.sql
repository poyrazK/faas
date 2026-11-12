-- filename: 20261103222943447_request_analytics_rollup_gaps.sql
-- migration-compat: expand
-- ADR-20261103113952 amendment 1: apid records each app minute whose
-- analytics rollups it lost (a flush Postgres refused for its data, or a
-- full buffer), so analytics answered from rollups report the window as
-- incomplete instead of under-counting silently. meterd sweeps markers past
-- the longest telemetry retention. The table is new; the previous release
-- neither reads nor writes it. Re-runnable.

-- +goose Up
SET LOCAL lock_timeout = '5s';

CREATE TABLE IF NOT EXISTS request_analytics_rollup_gaps (
    app_id uuid NOT NULL,
    bucket_start timestamptz NOT NULL CHECK (isfinite(bucket_start) AND bucket_start = date_bin('1 minute', bucket_start, 'epoch'::timestamptz)),
    PRIMARY KEY (app_id, bucket_start)
);
COMMENT ON TABLE request_analytics_rollup_gaps IS 'App minutes whose analytics rollups apid lost; written only by apid, swept only by meterd (ADR-20261103113952 amendment 1).';

CREATE INDEX IF NOT EXISTS request_analytics_rollup_gaps_bucket_idx
    ON request_analytics_rollup_gaps (bucket_start);

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TABLE IF EXISTS request_analytics_rollup_gaps;
