-- filename: 20261127072150375_api_sandbox_scenarios.sql
-- migration-compat: expand

-- +goose Up
SET LOCAL lock_timeout = '5s';

CREATE TABLE IF NOT EXISTS api_sandbox_scenarios (
    deployment_id uuid PRIMARY KEY REFERENCES deployments(id) ON DELETE CASCADE,
    revision uuid NOT NULL,
    definition bytea NOT NULL CHECK (octet_length(definition) BETWEEN 2 AND 24576)
);
COMMENT ON TABLE api_sandbox_scenarios IS 'The currently saved API sandbox scenario for a retained release. Each sandbox stores its own copy.';

ALTER TABLE api_sandbox_provisioning
    ADD COLUMN IF NOT EXISTS scenario bytea
    CHECK (scenario IS NULL OR octet_length(scenario) BETWEEN 2 AND 24704);

-- +goose Down
SET LOCAL lock_timeout = '5s';
ALTER TABLE api_sandbox_provisioning DROP COLUMN IF EXISTS scenario;
DROP TABLE IF EXISTS api_sandbox_scenarios;
