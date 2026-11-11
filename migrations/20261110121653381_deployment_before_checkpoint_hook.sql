-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
-- Record whether imaged baked a before_checkpoint hook into the deployment's
-- guest manifest (/etc/faas/app.json). The hook is an app-level setting, but
-- the guest only knows the hook its own layer was built with, so a park must
-- ask the guest for the callback only when that deployment declared it
-- (Hunt 32 H32-49). NULL means a deployment built before this column existed.
ALTER TABLE deployments
    ADD COLUMN IF NOT EXISTS before_checkpoint_hook boolean;

-- +goose Down
SET LOCAL lock_timeout = '5s';
ALTER TABLE deployments
    DROP COLUMN IF EXISTS before_checkpoint_hook;
