-- filename: 20260907090000002_deployment_inferred_profile.sql
-- +goose Up
-- +goose StatementBegin

ALTER TABLE deployments
    ADD COLUMN IF NOT EXISTS inferred_profile jsonb;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER TABLE deployments DROP COLUMN IF EXISTS inferred_profile;

-- +goose StatementEnd
