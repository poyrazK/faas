-- filename: 20261001030346991_gateway_traffic_runtime_observations.sql

-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS gateway_traffic_runtime_observations (
    node_name text PRIMARY KEY REFERENCES compute_nodes(name) ON DELETE CASCADE,
    generation bigserial NOT NULL UNIQUE CHECK (generation > 0),
    boot_id uuid NOT NULL,
    reported_at timestamptz,
    retry_enabled boolean NOT NULL DEFAULT false,
    rate_counter_mode text NOT NULL DEFAULT 'unwired' CHECK (rate_counter_mode IN ('central', 'local', 'unwired')),
    retry_counter_mode text NOT NULL DEFAULT 'unwired' CHECK (retry_counter_mode IN ('shared', 'redis', 'local', 'unwired')),
    retry_backend_id text NOT NULL DEFAULT '' CHECK (retry_backend_id = '' OR retry_backend_id ~ '^[0-9a-f]{16}$'),
    deadline_signing boolean NOT NULL DEFAULT false,
    policy_snapshot boolean NOT NULL DEFAULT false,
    security_revocation boolean NOT NULL DEFAULT false,
    managed_http boolean NOT NULL DEFAULT false,
    managed_circuit boolean NOT NULL DEFAULT false,
    CHECK (NOT managed_circuit OR managed_http),
    CHECK ((retry_counter_mode IN ('shared', 'redis') AND retry_backend_id <> '')
        OR (retry_counter_mode IN ('local', 'unwired') AND retry_backend_id = ''))
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE gateway_traffic_runtime_observations;
-- +goose StatementEnd
