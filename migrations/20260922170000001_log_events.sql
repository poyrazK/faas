-- +goose Up
-- +goose StatementBegin

-- ADR-213: durable, customer-queryable log projection. Source systems remain
-- authoritative; apid is the sole writer to this bounded tenant ledger.
CREATE TABLE IF NOT EXISTS log_events (
    id              uuid        NOT NULL DEFAULT gen_random_uuid(),
    occurred_at     timestamptz NOT NULL,
    account_id      uuid        NOT NULL,
    app_id          uuid        NOT NULL,
    deployment_id   uuid,
    instance_id     text        CHECK (instance_id IS NULL OR length(instance_id) BETWEEN 1 AND 256),
    source          text        NOT NULL CHECK (source IN ('runtime', 'build', 'deploy', 'http', 'network', 'dns')),
    source_event_id text        CHECK (source_event_id IS NULL OR length(source_event_id) BETWEEN 1 AND 256),
    request_id      text        CHECK (request_id IS NULL OR length(request_id) BETWEEN 1 AND 256),
    trace_id        text        CHECK (trace_id IS NULL OR length(trace_id) BETWEEN 1 AND 128),
    route           text        CHECK (route IS NULL OR length(route) BETWEEN 1 AND 256),
    method          text        CHECK (method IS NULL OR length(method) BETWEEN 1 AND 32),
    status          integer     CHECK (status IS NULL OR status BETWEEN 100 AND 599),
    level           text        CHECK (level IS NULL OR level IN ('trace', 'debug', 'info', 'warn', 'error', 'fatal')),
    stream          text        CHECK (stream IS NULL OR stream IN ('stdout', 'stderr', 'system', 'access')),
    message         text        NOT NULL CHECK (length(message) BETWEEN 1 AND 16384),
    latency_ms      integer     CHECK (latency_ms IS NULL OR latency_ms >= 0),
    occurrences     integer     NOT NULL DEFAULT 1 CHECK (occurrences > 0),
    cold_boot       boolean     NOT NULL DEFAULT false,
    fields          jsonb       NOT NULL DEFAULT '{}'::jsonb CHECK (
        jsonb_typeof(fields) = 'object' AND octet_length(fields::text) <= 32768
    ),
    PRIMARY KEY (id, occurred_at)
) PARTITION BY RANGE (occurred_at);

CREATE TABLE IF NOT EXISTS log_events_default
    PARTITION OF log_events DEFAULT;

-- Retries carry the same account, app, source event id, and original source
-- timestamp. Including occurred_at satisfies PostgreSQL's partitioned-unique
-- constraint while preserving idempotency for an identical replay.
CREATE UNIQUE INDEX IF NOT EXISTS log_events_source_dedupe_idx
    ON log_events (account_id, app_id, source, source_event_id, occurred_at)
    WHERE source_event_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS log_events_app_time_idx
    ON log_events (account_id, app_id, occurred_at DESC, id DESC);

CREATE INDEX IF NOT EXISTS log_events_app_source_time_idx
    ON log_events (account_id, app_id, source, occurred_at DESC, id DESC);

CREATE INDEX IF NOT EXISTS log_events_app_deployment_time_idx
    ON log_events (account_id, app_id, deployment_id, occurred_at DESC, id DESC)
    WHERE deployment_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS log_events_app_status_time_idx
    ON log_events (account_id, app_id, status, occurred_at DESC, id DESC)
    WHERE status IS NOT NULL;

CREATE INDEX IF NOT EXISTS log_events_app_route_time_idx
    ON log_events (account_id, app_id, route, occurred_at DESC, id DESC)
    WHERE route IS NOT NULL;

CREATE INDEX IF NOT EXISTS log_events_app_request_time_idx
    ON log_events (account_id, app_id, request_id, occurred_at DESC, id DESC)
    WHERE request_id IS NOT NULL;

-- Pre-create the current and next UTC month. The default partition is the
-- replay-safe catch-all; the retention/partition maintainer introduced by the
-- ingestion stack will keep the rolling partition window ahead of writers.
DO $$
DECLARE
    month_start timestamptz := date_trunc('month', now() AT TIME ZONE 'UTC') AT TIME ZONE 'UTC';
    partition_start timestamptz;
    partition_end timestamptz;
    partition_name text;
    offset_month integer;
BEGIN
    FOR offset_month IN 0..1 LOOP
        partition_start := month_start + make_interval(months => offset_month);
        partition_end := month_start + make_interval(months => offset_month + 1);
        partition_name := 'log_events_' || to_char(partition_start, 'YYYYMM');
        EXECUTE format(
            'CREATE TABLE IF NOT EXISTS %I PARTITION OF log_events FOR VALUES FROM (%L) TO (%L)',
            partition_name,
            partition_start,
            partition_end
        );
    END LOOP;
END $$;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS log_events;
-- +goose StatementEnd
