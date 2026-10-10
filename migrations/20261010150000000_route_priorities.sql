-- filename: 20261010150000000_route_priorities.sql

-- +goose Up
-- +goose StatementBegin
-- ADR-947: per-app route priorities for the warm-capacity queue. apid is the
-- only writer; gatewayd-internal reads them when a request has to queue.
CREATE TABLE IF NOT EXISTS app_route_priorities (
    app_id uuid PRIMARY KEY REFERENCES apps(id) ON DELETE CASCADE,
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    routes jsonb NOT NULL DEFAULT '[]'::jsonb
        CHECK (jsonb_typeof(routes) = 'array' AND jsonb_array_length(routes) <= 20),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK (isfinite(updated_at))
);
CREATE INDEX IF NOT EXISTS app_route_priorities_account_idx ON app_route_priorities (account_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS app_route_priorities;
-- +goose StatementEnd
