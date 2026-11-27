-- filename: 20261127072150443_api_sandbox_scenario_library.sql
-- migration-compat: expand

-- +goose Up
SET LOCAL lock_timeout = '5s';

CREATE TABLE IF NOT EXISTS app_api_sandbox_scenarios (
    app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    id uuid NOT NULL,
    revision uuid NOT NULL,
    name text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 100),
    definition bytea NOT NULL CHECK (octet_length(definition) BETWEEN 2 AND 24576),
    source_request_id uuid,
    PRIMARY KEY (app_id, id),
    UNIQUE (app_id, name),
    UNIQUE (app_id, revision)
);

-- +goose Down
DROP TABLE app_api_sandbox_scenarios;
