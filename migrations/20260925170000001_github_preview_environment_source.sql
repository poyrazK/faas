-- +goose Up
ALTER TABLE github_deploy_policies
    ADD COLUMN IF NOT EXISTS preview_environment_from text NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE github_deploy_policies
    DROP COLUMN IF EXISTS preview_environment_from;
