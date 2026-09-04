-- filename: 20260904212254300_workflow_definitions.sql

-- +goose Up
-- +goose StatementBegin

-- ADR-081: workflow definitions are versioned with the deployment that
-- introduced them. Existing deployments remain valid with an empty set.
ALTER TABLE deployments
    ADD COLUMN IF NOT EXISTS workflows jsonb NOT NULL DEFAULT '[]'::jsonb;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE deployments DROP COLUMN IF EXISTS workflows;
-- +goose StatementEnd
