-- filename: 20261122134943100_route_resource_minutes.sql
-- migration-compat: expand
-- ADR-20261121170000: route resource peaks. instance_resource_minutes holds
-- what each app instance's cgroup did per minute (vmmd folds guest-init's
-- ten-second slots); instance_route_minutes holds each instance's route
-- activity per minute (counted by the gateway). apid joins the two on
-- (app_id, instance_id, minute) to tell which routes the app's memory and
-- CPU peaks come from. Both are written only for apps the
-- route_resource_peaks platform flag releases and swept by meterd after
-- 14 days. Both tables are new, so the previous release neither reads nor
-- writes them. Re-runnable.

-- +goose Up
SET LOCAL lock_timeout = '5s';

CREATE TABLE IF NOT EXISTS instance_resource_minutes (
    app_id uuid NOT NULL,
    instance_id text NOT NULL CHECK (octet_length(instance_id) BETWEEN 1 AND 128),
    minute timestamptz NOT NULL CHECK (isfinite(minute) AND minute = date_bin('1 minute', minute, 'epoch'::timestamptz)),
    account_id uuid NOT NULL,
    deployment_id uuid NOT NULL,
    samples integer NOT NULL CHECK (samples >= 1),
    mem_start_mb integer NOT NULL CHECK (mem_start_mb >= 0),
    mem_peak_mb integer NOT NULL CHECK (mem_peak_mb >= mem_start_mb),
    mem_end_mb integer NOT NULL CHECK (mem_end_mb BETWEEN 0 AND mem_peak_mb),
    mem_limit_mb integer NOT NULL CHECK (mem_limit_mb >= 0),
    cpu_ms bigint NOT NULL CHECK (cpu_ms >= 0),
    cpu_peak_millicores integer NOT NULL CHECK (cpu_peak_millicores >= 0),
    cpu_limit_millicores integer NOT NULL CHECK (cpu_limit_millicores >= 0),
    PRIMARY KEY (app_id, minute, instance_id)
);
COMMENT ON TABLE instance_resource_minutes IS 'Per-instance app cgroup memory and CPU per minute; written by apid from vmmd, swept by meterd (ADR-20261121170000).';

CREATE INDEX IF NOT EXISTS instance_resource_minutes_minute_idx
    ON instance_resource_minutes (minute);

CREATE TABLE IF NOT EXISTS instance_route_minutes (
    app_id uuid NOT NULL,
    instance_id text NOT NULL CHECK (octet_length(instance_id) BETWEEN 1 AND 128),
    minute timestamptz NOT NULL CHECK (isfinite(minute) AND minute = date_bin('1 minute', minute, 'epoch'::timestamptz)),
    route text NOT NULL CHECK (octet_length(route) BETWEEN 1 AND 256),
    method text NOT NULL CHECK (octet_length(method) <= 16),
    account_id uuid NOT NULL,
    requests bigint NOT NULL CHECK (requests >= 0),
    busy_ms bigint NOT NULL CHECK (busy_ms >= 0),
    max_latency_ms integer NOT NULL CHECK (max_latency_ms >= 0),
    PRIMARY KEY (app_id, minute, instance_id, route, method)
);
COMMENT ON TABLE instance_route_minutes IS 'Per-instance route activity per minute; written by apid from gateways, swept by meterd (ADR-20261121170000).';

CREATE INDEX IF NOT EXISTS instance_route_minutes_minute_idx
    ON instance_route_minutes (minute);

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TABLE IF EXISTS instance_route_minutes;
DROP TABLE IF EXISTS instance_resource_minutes;
