-- filename: 20261103114054894_request_analytics_rollups.sql
-- migration-compat: expand
-- ADR-20261103113952: per-minute request analytics rollups. apid aggregates
-- every telemetry row a plan keeps (before the per-account debugger ingest
-- cap) and adds it here every few seconds; meterd sweeps rows past the plan's
-- telemetry retention. request_analytics_rollup_coverage records the first
-- minute each app's rollups hold, so readers fall back to request_telemetry
-- for older windows. Both tables are new, so the previous release neither
-- reads nor writes them. Re-runnable.

-- +goose Up
SET LOCAL lock_timeout = '5s';

CREATE TABLE IF NOT EXISTS request_analytics_minutes (
    app_id uuid NOT NULL,
    bucket_start timestamptz NOT NULL CHECK (isfinite(bucket_start) AND bucket_start = date_bin('1 minute', bucket_start, 'epoch'::timestamptz)),
    dimension_kind text NOT NULL CHECK (dimension_kind IN ('route', 'country', 'referrer_host', 'ua_family', 'consumer_id')),
    dimension text NOT NULL CHECK (octet_length(dimension) BETWEEN 1 AND 512),
    method text NOT NULL CHECK (octet_length(method) <= 16 AND (dimension_kind = 'route' OR method = '')),
    status smallint NOT NULL CHECK (status BETWEEN 100 AND 599),
    deployment_id uuid NOT NULL,
    account_id uuid NOT NULL,
    requests bigint NOT NULL CHECK (requests >= 1),
    error_requests bigint NOT NULL CHECK (error_requests BETWEEN 0 AND requests),
    cold_boots bigint NOT NULL CHECK (cold_boots BETWEEN 0 AND requests),
    -- Sum of latency_ms x requests, for analytics' request-time allocation.
    request_time_ms bigint NOT NULL CHECK (request_time_ms >= 0),
    latency_max_ms integer NOT NULL CHECK (latency_max_ms >= 0),
    -- Counts per api.RequestAnalyticsLatencyBoundsMS bucket, plus one
    -- overflow bucket. The width is fixed; the writer and readers check it.
    latency_hist bigint[] NOT NULL CHECK (cardinality(latency_hist) = 45 AND array_ndims(latency_hist) = 1 AND array_position(latency_hist, NULL) IS NULL),
    PRIMARY KEY (app_id, bucket_start, dimension_kind, dimension, method, status, deployment_id)
);
COMMENT ON TABLE request_analytics_minutes IS 'Per-minute request analytics rollups; added to only by apid, swept only by meterd (ADR-20261103113952).';

CREATE INDEX IF NOT EXISTS request_analytics_minutes_bucket_idx
    ON request_analytics_minutes (bucket_start);

CREATE TABLE IF NOT EXISTS request_analytics_rollup_coverage (
    app_id uuid PRIMARY KEY REFERENCES apps(id) ON DELETE CASCADE,
    covered_since timestamptz NOT NULL CHECK (isfinite(covered_since) AND covered_since = date_bin('1 minute', covered_since, 'epoch'::timestamptz))
);
COMMENT ON TABLE request_analytics_rollup_coverage IS 'First minute held in request_analytics_minutes per app; written only by apid (ADR-20261103113952).';

-- Element-wise sum for the additive upsert. unnest pads a shorter array
-- with NULLs, which the latency_hist CHECK rejects, so a mismatched writer
-- fails loudly instead of truncating.
CREATE OR REPLACE FUNCTION request_analytics_hist_add(a bigint[], b bigint[])
RETURNS bigint[]
LANGUAGE sql
IMMUTABLE
STRICT
PARALLEL SAFE
AS $$
    SELECT array_agg(x + y ORDER BY i) FROM unnest(a, b) WITH ORDINALITY AS u(x, y, i)
$$;

-- Element-wise sum aggregate for readers: one merged histogram per group.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION request_analytics_hist_acc(acc bigint[], v bigint[])
RETURNS bigint[]
LANGUAGE plpgsql
IMMUTABLE
PARALLEL SAFE
AS $$
BEGIN
    IF v IS NULL THEN
        RETURN acc;
    END IF;
    IF acc IS NULL THEN
        RETURN v;
    END IF;
    IF cardinality(acc) <> cardinality(v) THEN
        RAISE EXCEPTION 'request analytics histograms differ in width: % and %', cardinality(acc), cardinality(v);
    END IF;
    FOR i IN 1 .. cardinality(v) LOOP
        acc[i] := acc[i] + v[i];
    END LOOP;
    RETURN acc;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $hist_sum$
BEGIN
    IF to_regprocedure('request_analytics_hist_sum(bigint[])') IS NULL THEN
        CREATE AGGREGATE request_analytics_hist_sum(bigint[]) (
            SFUNC = request_analytics_hist_acc,
            STYPE = bigint[]
        );
    END IF;
END
$hist_sum$;
-- +goose StatementEnd

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TABLE IF EXISTS request_analytics_rollup_coverage;
DROP TABLE IF EXISTS request_analytics_minutes;
DROP AGGREGATE IF EXISTS request_analytics_hist_sum(bigint[]);
DROP FUNCTION IF EXISTS request_analytics_hist_acc(bigint[], bigint[]);
DROP FUNCTION IF EXISTS request_analytics_hist_add(bigint[], bigint[]);
