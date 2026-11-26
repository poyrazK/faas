-- filename: 20261126195421836_api_sandbox_scenario_origin.sql
-- migration-compat: expand

-- +goose Up
SET LOCAL lock_timeout = '5s';

ALTER TABLE api_sandbox_scenarios ADD COLUMN IF NOT EXISTS source_request_id uuid;

-- +goose Down
ALTER TABLE api_sandbox_scenarios DROP COLUMN source_request_id;
