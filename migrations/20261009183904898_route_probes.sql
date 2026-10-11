-- filename: 20261009183904898_route_probes.sql

-- +goose Up
-- +goose StatementBegin
-- ADR-847: synthetic route probes record their own per-minute results here.
-- Probe requests are never written to request_telemetry or usage, so organic
-- analytics, budgets and customer reach exclude them by construction.
CREATE TABLE IF NOT EXISTS route_probe_observations (
    app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    account_id uuid NOT NULL,
    deployment_id uuid NOT NULL,
    method text NOT NULL CHECK (method IN ('GET', 'HEAD')),
    path text NOT NULL CHECK (octet_length(path) BETWEEN 1 AND 240),
    window_start timestamptz NOT NULL CHECK (isfinite(window_start)),
    requests bigint NOT NULL DEFAULT 0 CHECK (requests >= 0),
    server_errors bigint NOT NULL DEFAULT 0 CHECK (server_errors >= 0),
    unauthenticated bigint NOT NULL DEFAULT 0 CHECK (unauthenticated >= 0),
    CHECK (server_errors + unauthenticated <= requests),
    PRIMARY KEY (app_id, deployment_id, method, path, window_start)
);
CREATE INDEX IF NOT EXISTS route_probe_observations_window_idx ON route_probe_observations (window_start);

-- One probe round per app and minute across apid replicas.
CREATE TABLE IF NOT EXISTS route_probe_rounds (
    app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    window_start timestamptz NOT NULL CHECK (isfinite(window_start)),
    claimed_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK (isfinite(claimed_at)),
    PRIMARY KEY (app_id, window_start)
);
CREATE INDEX IF NOT EXISTS route_probe_rounds_window_idx ON route_probe_rounds (window_start);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS route_probe_rounds;
DROP TABLE IF EXISTS route_probe_observations;
-- +goose StatementEnd
