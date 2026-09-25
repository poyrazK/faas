-- +goose Up
-- +goose StatementBegin
-- Exact completed-request evidence, separate from the lossy debugger buckets.
-- The usage event ID is the replay key. Account deletion removes both the
-- financial and audit rows; no query, header or body is stored. Source IP is
-- optional and verified at the public-gateway handoff.
-- Undeclared route candidates may still include literal path segments.
CREATE TABLE request_audit_events (
    event_id uuid PRIMARY KEY REFERENCES api_consumer_usage_events(event_id) ON DELETE CASCADE,
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    consumer_key text NOT NULL,
    platform_tenant_id uuid,
    route_template text NOT NULL CHECK (length(route_template) BETWEEN 1 AND 256),
    method text NOT NULL CHECK (length(method) BETWEEN 1 AND 16),
    http_status integer NOT NULL CHECK (http_status BETWEEN 100 AND 599),
    latency_ms integer NOT NULL CHECK (latency_ms BETWEEN 0 AND 86400000),
    trace_id text NOT NULL DEFAULT '' CHECK (trace_id = '' OR length(trace_id) = 32),
    deployment_id uuid,
    commit_sha text NOT NULL DEFAULT '' CHECK (length(commit_sha) <= 64),
    occurred_at timestamptz NOT NULL,
    request_id text NOT NULL DEFAULT '' CHECK (length(request_id) <= 128),
    source_ip inet,
    received_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX request_audit_events_app_time_idx
    ON request_audit_events (account_id, app_id, occurred_at DESC, event_id DESC);
CREATE INDEX request_audit_events_app_route_idx
    ON request_audit_events (account_id, app_id, route_template, occurred_at DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS request_audit_events;
-- +goose StatementEnd
