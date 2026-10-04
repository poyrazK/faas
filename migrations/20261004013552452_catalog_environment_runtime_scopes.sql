-- ADR-568: catalog environment names are usable throughout runtime intent.
-- Existing scope values, IDs, sealed data and queue receipt identity are retained.
-- Legacy runtime scopes retain their 40-character bound; catalog-owned rows use
-- the catalog 33-character bound. Empty shared queues and the default runtime
-- scope retain their existing semantics. The read-only __all__ sentinel is invalid.
-- +goose Up
ALTER TABLE app_envs DROP CONSTRAINT IF EXISTS app_envs_scope_shape;
ALTER TABLE app_envs ADD CONSTRAINT app_envs_scope_shape CHECK (scope ~ '^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$');
ALTER TABLE app_secrets DROP CONSTRAINT IF EXISTS app_secrets_scope_shape;
ALTER TABLE app_secrets ADD CONSTRAINT app_secrets_scope_shape CHECK (scope ~ '^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$');
ALTER TABLE deployments DROP CONSTRAINT IF EXISTS deployments_scope_shape;
ALTER TABLE deployments ADD CONSTRAINT deployments_scope_shape CHECK (scope ~ '^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$');
ALTER TABLE invocations DROP CONSTRAINT IF EXISTS invocation_deployment_scope_check;
ALTER TABLE invocations ADD CONSTRAINT invocation_deployment_scope_check CHECK (deployment_scope ~ '^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$');
ALTER TABLE data_upstreams DROP CONSTRAINT IF EXISTS data_upstreams_scope_check;
ALTER TABLE data_upstreams ADD CONSTRAINT data_upstreams_scope_check CHECK (scope ~ '^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$');
ALTER TABLE data_upstreams DROP CONSTRAINT IF EXISTS data_upstreams_deployment_scope_shape;
ALTER TABLE data_upstreams ADD CONSTRAINT data_upstreams_deployment_scope_shape CHECK (deployment_scope ~ '^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$');
ALTER TABLE deployment_openapi_snapshots DROP CONSTRAINT IF EXISTS deployment_openapi_snapshots_scope_shape;
ALTER TABLE deployment_openapi_snapshots ADD CONSTRAINT deployment_openapi_snapshots_scope_shape CHECK (scope ~ '^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$');
ALTER TABLE github_deploy_branches DROP CONSTRAINT IF EXISTS github_deploy_branches_scope_check;
ALTER TABLE github_deploy_branches ADD CONSTRAINT github_deploy_branches_scope_check CHECK (scope ~ '^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$');
ALTER TABLE app_tasks DROP CONSTRAINT IF EXISTS app_tasks_scope_chk;
ALTER TABLE app_tasks ADD CONSTRAINT app_tasks_scope_chk CHECK (deployment_scope ~ '^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$');
ALTER TABLE app_secret_revocations DROP CONSTRAINT IF EXISTS app_secret_revocations_scope_shape;
ALTER TABLE app_secret_revocations ADD CONSTRAINT app_secret_revocations_scope_shape CHECK (scope ~ '^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$');
ALTER TABLE object_storage_s3_credentials DROP CONSTRAINT IF EXISTS object_storage_s3_credentials_managed_shape_check;
ALTER TABLE object_storage_s3_credentials ADD CONSTRAINT object_storage_s3_credentials_managed_shape_check CHECK (((managed_app_id IS NULL AND managed_scope IS NULL AND managed_prefix IS NULL) OR (managed_app_id IS NOT NULL AND managed_scope ~ '^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$' AND managed_prefix ~ '^[A-Z][A-Z0-9_]{0,47}$')));
ALTER TABLE queue_bindings DROP CONSTRAINT IF EXISTS queue_binding_scope_shape;
ALTER TABLE queue_bindings ADD CONSTRAINT queue_binding_scope_shape CHECK (((deployment_scope='' AND environment_id IS NULL) OR (deployment_scope ~ '^[a-z0-9]([a-z0-9-]{0,31}[a-z0-9])?$' AND environment_id IS NOT NULL)));
ALTER TABLE triggers DROP CONSTRAINT IF EXISTS queue_consumer_scope_shape;
ALTER TABLE triggers ADD CONSTRAINT queue_consumer_scope_shape CHECK (((queue_binding_scope='' AND queue_binding_environment_id IS NULL) OR (queue_binding_id IS NOT NULL AND queue_binding_scope ~ '^[a-z0-9]([a-z0-9-]{0,31}[a-z0-9])?$' AND queue_binding_environment_id IS NOT NULL)));
ALTER TABLE app_environment_secret_refs DROP CONSTRAINT IF EXISTS app_environment_secret_refs_scope_check;
ALTER TABLE app_environment_secret_refs ADD CONSTRAINT app_environment_secret_refs_scope_check CHECK (scope ~ '^[a-z0-9]([a-z0-9-]{0,31}[a-z0-9])?$');
ALTER TABLE app_environment_secret_ref_suppressions DROP CONSTRAINT IF EXISTS app_environment_secret_ref_suppressions_scope_check;
ALTER TABLE app_environment_secret_ref_suppressions ADD CONSTRAINT app_environment_secret_ref_suppressions_scope_check CHECK (scope ~ '^[a-z0-9]([a-z0-9-]{0,31}[a-z0-9])?$');

-- Down deliberately fails atomically if new short/numeric catalog data would
-- violate a historical constraint. It never deletes or rewrites accepted data.
-- +goose Down
ALTER TABLE app_environment_secret_ref_suppressions DROP CONSTRAINT IF EXISTS app_environment_secret_ref_suppressions_scope_check;
ALTER TABLE app_environment_secret_ref_suppressions ADD CONSTRAINT app_environment_secret_ref_suppressions_scope_check CHECK (scope ~ '^[a-z][a-z0-9-]{0,62}$');
ALTER TABLE app_environment_secret_refs DROP CONSTRAINT IF EXISTS app_environment_secret_refs_scope_check;
ALTER TABLE app_environment_secret_refs ADD CONSTRAINT app_environment_secret_refs_scope_check CHECK (scope ~ '^[a-z][a-z0-9-]{0,62}$');
ALTER TABLE triggers DROP CONSTRAINT IF EXISTS queue_consumer_scope_shape;
ALTER TABLE triggers ADD CONSTRAINT queue_consumer_scope_shape CHECK (((queue_binding_scope='' AND queue_binding_environment_id IS NULL) OR (queue_binding_id IS NOT NULL AND queue_binding_scope ~ '^[a-z][a-z0-9-]{0,62}$' AND queue_binding_environment_id IS NOT NULL)));
ALTER TABLE queue_bindings DROP CONSTRAINT IF EXISTS queue_binding_scope_shape;
ALTER TABLE queue_bindings ADD CONSTRAINT queue_binding_scope_shape CHECK (((deployment_scope='' AND environment_id IS NULL) OR (deployment_scope ~ '^[a-z][a-z0-9-]{0,62}$' AND environment_id IS NOT NULL)));
ALTER TABLE object_storage_s3_credentials DROP CONSTRAINT IF EXISTS object_storage_s3_credentials_managed_shape_check;
ALTER TABLE object_storage_s3_credentials ADD CONSTRAINT object_storage_s3_credentials_managed_shape_check CHECK (((managed_app_id IS NULL AND managed_scope IS NULL AND managed_prefix IS NULL) OR (managed_app_id IS NOT NULL AND managed_scope ~ '^[a-z0-9]([a-z0-9-]{1,38})[a-z0-9]$' AND managed_prefix ~ '^[A-Z][A-Z0-9_]{0,47}$')));
ALTER TABLE app_secret_revocations DROP CONSTRAINT IF EXISTS app_secret_revocations_scope_shape;
ALTER TABLE app_secret_revocations ADD CONSTRAINT app_secret_revocations_scope_shape CHECK (scope ~ '^[a-z0-9]([a-z0-9-]{1,38})[a-z0-9]$');
ALTER TABLE app_tasks DROP CONSTRAINT IF EXISTS app_tasks_scope_chk;
ALTER TABLE app_tasks ADD CONSTRAINT app_tasks_scope_chk CHECK (deployment_scope ~ '^[a-z0-9]([a-z0-9-]{1,38})[a-z0-9]$');
ALTER TABLE github_deploy_branches DROP CONSTRAINT IF EXISTS github_deploy_branches_scope_check;
ALTER TABLE github_deploy_branches ADD CONSTRAINT github_deploy_branches_scope_check CHECK (scope ~ '^[a-z0-9]([a-z0-9-]{1,38})[a-z0-9]$');
ALTER TABLE deployment_openapi_snapshots DROP CONSTRAINT IF EXISTS deployment_openapi_snapshots_scope_shape;
ALTER TABLE deployment_openapi_snapshots ADD CONSTRAINT deployment_openapi_snapshots_scope_shape CHECK (scope ~ '^[a-z0-9]([a-z0-9-]{1,38})[a-z0-9]$');
ALTER TABLE data_upstreams DROP CONSTRAINT IF EXISTS data_upstreams_deployment_scope_shape;
ALTER TABLE data_upstreams ADD CONSTRAINT data_upstreams_deployment_scope_shape CHECK (deployment_scope ~ '^[a-z0-9]([a-z0-9-]{1,38})[a-z0-9]$');
ALTER TABLE data_upstreams DROP CONSTRAINT IF EXISTS data_upstreams_scope_check;
ALTER TABLE data_upstreams ADD CONSTRAINT data_upstreams_scope_check CHECK (scope ~ '^[a-z0-9]([a-z0-9-]{1,38})[a-z0-9]$');
ALTER TABLE invocations DROP CONSTRAINT IF EXISTS invocation_deployment_scope_check;
ALTER TABLE invocations ADD CONSTRAINT invocation_deployment_scope_check CHECK (deployment_scope ~ '^[a-z0-9]([a-z0-9-]{1,38})[a-z0-9]$');
ALTER TABLE deployments DROP CONSTRAINT IF EXISTS deployments_scope_shape;
ALTER TABLE deployments ADD CONSTRAINT deployments_scope_shape CHECK (scope ~ '^[a-z0-9]([a-z0-9-]{1,38})[a-z0-9]$');
ALTER TABLE app_secrets DROP CONSTRAINT IF EXISTS app_secrets_scope_shape;
ALTER TABLE app_secrets ADD CONSTRAINT app_secrets_scope_shape CHECK (scope ~ '^[a-z0-9]([a-z0-9-]{1,38})[a-z0-9]$');
ALTER TABLE app_envs DROP CONSTRAINT IF EXISTS app_envs_scope_shape;
ALTER TABLE app_envs ADD CONSTRAINT app_envs_scope_shape CHECK (scope ~ '^[a-z0-9]([a-z0-9-]{1,38})[a-z0-9]$');
