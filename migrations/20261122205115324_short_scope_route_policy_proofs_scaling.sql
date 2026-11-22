-- filename: 20261122205115324_short_scope_route_policy_proofs_scaling.sql
-- Deployment scopes are 1-40 characters (deployments_scope_shape), and a
-- project environment may be named with one or two (api.ValidProjectEnvironmentSlug).
-- The route policy snapshot, runtime config proof and environment scaling
-- tables required at least three, so a deployment in a one- or two-character
-- environment (an environment branch named "b1") never went live: imaged's
-- mark-live failed persisting its route policy snapshot and retried forever.
-- Same repair as 20261018162329975 for the deployment mail tables.
-- migration-compat: expand

-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE deployment_route_policy_snapshots DROP CONSTRAINT deployment_route_policy_snapshots_scope_shape;
ALTER TABLE deployment_route_policy_snapshots ADD CONSTRAINT deployment_route_policy_snapshots_scope_shape
    CHECK (scope ~ '^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$');
ALTER TABLE runtime_instance_config_proofs DROP CONSTRAINT runtime_instance_config_proofs_scope_check;
ALTER TABLE runtime_instance_config_proofs ADD CONSTRAINT runtime_instance_config_proofs_scope_check
    CHECK (scope = 'default' OR scope ~ '^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$');
ALTER TABLE runtime_environment_scaling_states DROP CONSTRAINT runtime_environment_scaling_states_scope_check;
ALTER TABLE runtime_environment_scaling_states ADD CONSTRAINT runtime_environment_scaling_states_scope_check
    CHECK (scope ~ '^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$' AND scope <> 'default');

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS(SELECT 1 FROM deployment_route_policy_snapshots WHERE length(scope)<3)
        OR EXISTS(SELECT 1 FROM runtime_instance_config_proofs WHERE length(scope)<3)
        OR EXISTS(SELECT 1 FROM runtime_environment_scaling_states WHERE length(scope)<3) THEN
        RAISE EXCEPTION 'short deployment scopes prevent downgrade';
    END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE deployment_route_policy_snapshots DROP CONSTRAINT deployment_route_policy_snapshots_scope_shape;
ALTER TABLE deployment_route_policy_snapshots ADD CONSTRAINT deployment_route_policy_snapshots_scope_shape
    CHECK (scope ~ '^[a-z0-9]([a-z0-9-]{1,38})[a-z0-9]$');
ALTER TABLE runtime_instance_config_proofs DROP CONSTRAINT runtime_instance_config_proofs_scope_check;
ALTER TABLE runtime_instance_config_proofs ADD CONSTRAINT runtime_instance_config_proofs_scope_check
    CHECK (scope = 'default' OR scope ~ '^[a-z0-9]([a-z0-9-]{1,38})[a-z0-9]$');
ALTER TABLE runtime_environment_scaling_states DROP CONSTRAINT runtime_environment_scaling_states_scope_check;
ALTER TABLE runtime_environment_scaling_states ADD CONSTRAINT runtime_environment_scaling_states_scope_check
    CHECK (scope ~ '^[a-z0-9]([a-z0-9-]{1,38})[a-z0-9]$' AND scope <> 'default');
