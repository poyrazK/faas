-- filename: 20261122205115324_short_scope_route_policy_proofs_scaling.sql
-- migration-compat: expand
-- Deployment scopes are 1-40 characters (deployments_scope_shape), and a
-- project environment may be named with one or two (api.ValidProjectEnvironmentSlug).
-- The route policy snapshot, runtime config proof and environment scaling
-- tables required at least three, so a deployment in a one- or two-character
-- environment (an environment branch named "b1") never went live: imaged's
-- mark-live failed persisting its route policy snapshot and retried forever.
-- Same repair as 20261018162329975 for the deployment mail tables. Each
-- widened CHECK is added under a new name NOT VALID and validated before the
-- old one is dropped. Every step is re-runnable.
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';

-- +goose StatementBegin
DO $add$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'deployment_route_policy_snapshots'::regclass AND conname = 'deployment_route_policy_snapshots_scope_v2_check') THEN
    ALTER TABLE deployment_route_policy_snapshots ADD CONSTRAINT deployment_route_policy_snapshots_scope_v2_check
      CHECK (scope ~ '^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$') NOT VALID;
  END IF;
END
$add$;
-- +goose StatementEnd

ALTER TABLE deployment_route_policy_snapshots VALIDATE CONSTRAINT deployment_route_policy_snapshots_scope_v2_check;
ALTER TABLE deployment_route_policy_snapshots DROP CONSTRAINT IF EXISTS deployment_route_policy_snapshots_scope_shape;

-- +goose StatementBegin
DO $add$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'runtime_instance_config_proofs'::regclass AND conname = 'runtime_instance_config_proofs_scope_v2_check') THEN
    ALTER TABLE runtime_instance_config_proofs ADD CONSTRAINT runtime_instance_config_proofs_scope_v2_check
      CHECK (scope = 'default' OR scope ~ '^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$') NOT VALID;
  END IF;
END
$add$;
-- +goose StatementEnd

ALTER TABLE runtime_instance_config_proofs VALIDATE CONSTRAINT runtime_instance_config_proofs_scope_v2_check;
ALTER TABLE runtime_instance_config_proofs DROP CONSTRAINT IF EXISTS runtime_instance_config_proofs_scope_check;

-- +goose StatementBegin
DO $add$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'runtime_environment_scaling_states'::regclass AND conname = 'runtime_environment_scaling_states_scope_v2_check') THEN
    ALTER TABLE runtime_environment_scaling_states ADD CONSTRAINT runtime_environment_scaling_states_scope_v2_check
      CHECK (scope ~ '^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$' AND scope <> 'default') NOT VALID;
  END IF;
END
$add$;
-- +goose StatementEnd

ALTER TABLE runtime_environment_scaling_states VALIDATE CONSTRAINT runtime_environment_scaling_states_scope_v2_check;
ALTER TABLE runtime_environment_scaling_states DROP CONSTRAINT IF EXISTS runtime_environment_scaling_states_scope_check;

RESET lock_timeout;

-- +goose Down
-- Forward-only: restoring the three-character checks would fail every row of
-- a short-scope deployment.
SELECT 1;
