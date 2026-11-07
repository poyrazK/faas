-- filename: 20261107093030512_request_analytics_hours.sql
-- migration-compat: expand
-- ADR-20261103113952 amendment 2: an hourly rollup tier. apid adds every
-- flush to request_analytics_hours as well as request_analytics_minutes, and
-- long analytics windows read whole hours from it, so a 14-day window reads
-- 336 buckets per group instead of 20,160. request_analytics_rollup_coverage
-- gains hours_covered_since, the first minute this tier holds: readers use
-- an hour only once it starts at or after the hour following that minute.
-- The previous release neither reads nor writes either. The new column is
-- nullable without a default (catalog-only), and its CHECK is added NOT VALID
-- and validated separately (SHARE UPDATE EXCLUSIVE), so coverage keeps being
-- written. Every step is re-runnable.
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';

CREATE TABLE IF NOT EXISTS request_analytics_hours (
    app_id uuid NOT NULL,
    bucket_start timestamptz NOT NULL CHECK (isfinite(bucket_start) AND bucket_start = date_bin('1 hour', bucket_start, 'epoch'::timestamptz)),
    dimension_kind text NOT NULL CHECK (dimension_kind IN ('route', 'country', 'referrer_host', 'ua_family', 'consumer_id')),
    dimension text NOT NULL CHECK (octet_length(dimension) BETWEEN 1 AND 512),
    method text NOT NULL CHECK (octet_length(method) <= 16 AND (dimension_kind = 'route' OR method = '')),
    status smallint NOT NULL CHECK (status BETWEEN 100 AND 599),
    deployment_id uuid NOT NULL,
    account_id uuid NOT NULL,
    requests bigint NOT NULL CHECK (requests >= 1),
    error_requests bigint NOT NULL CHECK (error_requests BETWEEN 0 AND requests),
    cold_boots bigint NOT NULL CHECK (cold_boots BETWEEN 0 AND requests),
    request_time_ms bigint NOT NULL CHECK (request_time_ms >= 0),
    latency_max_ms integer NOT NULL CHECK (latency_max_ms >= 0),
    latency_hist bigint[] NOT NULL CHECK (cardinality(latency_hist) = 45 AND array_ndims(latency_hist) = 1 AND array_position(latency_hist, NULL) IS NULL),
    PRIMARY KEY (app_id, bucket_start, dimension_kind, dimension, method, status, deployment_id)
);
COMMENT ON TABLE request_analytics_hours IS 'Hourly request analytics rollups; added to only by apid, swept only by meterd (ADR-20261103113952 amendment 2).';

CREATE INDEX IF NOT EXISTS request_analytics_hours_bucket_idx
    ON request_analytics_hours (bucket_start);

ALTER TABLE request_analytics_rollup_coverage ADD COLUMN IF NOT EXISTS hours_covered_since timestamptz;

-- +goose StatementBegin
DO $hours_covered$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'request_analytics_rollup_coverage'::regclass AND conname = 'request_analytics_rollup_coverage_hours_covered_since_chk') THEN
    ALTER TABLE request_analytics_rollup_coverage ADD CONSTRAINT request_analytics_rollup_coverage_hours_covered_since_chk
      CHECK (hours_covered_since IS NULL OR (isfinite(hours_covered_since) AND hours_covered_since = date_bin('1 minute', hours_covered_since, 'epoch'::timestamptz))) NOT VALID;
  END IF;
END
$hours_covered$;
-- +goose StatementEnd

ALTER TABLE request_analytics_rollup_coverage VALIDATE CONSTRAINT request_analytics_rollup_coverage_hours_covered_since_chk;

-- +goose Down
SET lock_timeout = '5s';
ALTER TABLE request_analytics_rollup_coverage DROP CONSTRAINT IF EXISTS request_analytics_rollup_coverage_hours_covered_since_chk;
ALTER TABLE request_analytics_rollup_coverage DROP COLUMN IF EXISTS hours_covered_since;
DROP TABLE IF EXISTS request_analytics_hours;
