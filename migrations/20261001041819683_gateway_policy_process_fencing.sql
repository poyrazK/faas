-- filename: 20261001041819683_gateway_policy_process_fencing.sql

-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS gateway_traffic_policy_observations (
    node_name text NOT NULL REFERENCES compute_nodes(name) ON DELETE CASCADE,
    policy_kind text NOT NULL CHECK (policy_kind IN ('control_plane', 'edge_rules', 'cors_presets', 'cache_purge')),
    generation bigint NOT NULL CHECK (generation > 0),
    boot_id uuid NOT NULL,
    last_change_id bigint NOT NULL CHECK (last_change_id >= 0),
    observed_at timestamptz NOT NULL,
    PRIMARY KEY (node_name, policy_kind)
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE gateway_traffic_policy_observations;
-- +goose StatementEnd
