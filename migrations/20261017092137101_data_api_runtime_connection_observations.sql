-- filename: 20261017092137101_data_api_runtime_connection_observations.sql

-- +goose Up
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS data_api_runtime_connection_observations (
    binding_id uuid NOT NULL REFERENCES managed_postgres_bindings(id) ON DELETE CASCADE,
    instance_id uuid NOT NULL REFERENCES instances(id) ON DELETE CASCADE,
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    database_id uuid NOT NULL REFERENCES managed_postgres_databases(id) ON DELETE CASCADE,
    deployment_id uuid NOT NULL REFERENCES deployments(id) ON DELETE CASCADE,
    node_id uuid NOT NULL REFERENCES compute_nodes(id) ON DELETE CASCADE,
    wake_id uuid NOT NULL,
    backend_fingerprint text NOT NULL,
    data_resource_id text NOT NULL,
    database_generation bigint NOT NULL CHECK (database_generation > 0),
    credential_generation bigint NOT NULL CHECK (credential_generation > 0),
    provider_identity_id text NOT NULL,
    state text NOT NULL CHECK (state IN ('observed','unknown')),
    runtime_model text NOT NULL CHECK (runtime_model = 'postgrest-14.3-pools-v1'),
    engine_connections integer NOT NULL CHECK (engine_connections BETWEEN 1 AND 2),
    refresh_connections integer NOT NULL CHECK (refresh_connections BETWEEN 0 AND 1),
    observed_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    PRIMARY KEY (binding_id, instance_id),
    CHECK (engine_connections + refresh_connections = 2),
    CHECK (expires_at > observed_at AND expires_at <= observed_at + interval '30 seconds')
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS data_api_runtime_connection_observations;
-- +goose StatementEnd
