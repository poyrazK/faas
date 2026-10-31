-- filename: 20261030205907043_agent_version_pause_during_model_calls.sql
-- migration-compat: expand

-- +goose Up
-- A constant default adds the column without rewriting agent_versions, which
-- is still dark-launched and small.
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
-- ADR-20261027090012 §1: an agent version can opt out of the model-wait pause,
-- for a harness whose connections are heartbeat-sensitive. Existing versions
-- keep the default: the pause is allowed.
ALTER TABLE agent_versions
    ADD COLUMN IF NOT EXISTS pause_during_model_calls boolean NOT NULL DEFAULT true;
-- +goose StatementEnd

-- +goose Down
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
ALTER TABLE agent_versions DROP COLUMN IF EXISTS pause_during_model_calls;
-- +goose StatementEnd
