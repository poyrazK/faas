-- +goose Up
-- Persist the immutable application-level reload opt-in from the deployment
-- image manifest. NULL means a deployment created before this field existed;
-- an empty string is an explicit deployment with no reload support.
ALTER TABLE deployments
    ADD COLUMN IF NOT EXISTS secret_reload_signal text;

ALTER TABLE deployments
    DROP CONSTRAINT IF EXISTS deployments_secret_reload_signal_chk;

ALTER TABLE deployments
    ADD CONSTRAINT deployments_secret_reload_signal_chk
    CHECK (secret_reload_signal IS NULL OR secret_reload_signal IN ('', 'SIGHUP', 'SIGUSR1', 'SIGUSR2'));

-- +goose Down
ALTER TABLE deployments
    DROP CONSTRAINT IF EXISTS deployments_secret_reload_signal_chk;

ALTER TABLE deployments
    DROP COLUMN IF EXISTS secret_reload_signal;
