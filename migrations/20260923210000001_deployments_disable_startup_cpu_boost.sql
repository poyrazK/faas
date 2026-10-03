-- Allow a deployment to opt out of the bounded startup CPU allowance.
-- The default preserves the boost for all existing and new deployments.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE deployments
    ADD COLUMN IF NOT EXISTS disable_startup_cpu_boost boolean NOT NULL DEFAULT false;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE deployments
    DROP COLUMN IF EXISTS disable_startup_cpu_boost;
-- +goose StatementEnd
