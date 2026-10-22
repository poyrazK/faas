-- +goose Up
-- ADR-590: atomic removal of unborrowed original environment metadata.
CREATE TABLE IF NOT EXISTS project_environment_clone_environment_cleanups (
 operation_id uuid PRIMARY KEY REFERENCES project_environment_clone_workload_cleanups(operation_id) ON DELETE RESTRICT,
 environment_id uuid NOT NULL,
 workload_cleanup_sha256 text NOT NULL CHECK(workload_cleanup_sha256 ~ '^[a-f0-9]{64}$'),
 manifest jsonb NOT NULL CHECK(jsonb_typeof(manifest)='object' AND octet_length(manifest::text)<=32768),
 manifest_sha256 text NOT NULL CHECK(manifest_sha256 ~ '^[a-f0-9]{64}$'),
 cleaned_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION lock_clone_environment_cleanup_scope_borrowers() RETURNS void
LANGUAGE plpgsql AS $$ BEGIN
 LOCK TABLE app_binding_release_policies,app_environment_secret_ref_suppressions,app_environment_secret_refs,app_envs,app_issues,app_secret_runtime_reload_observations,app_secrets,app_tasks,app_webhooks,customer_operation_definitions,customer_operation_workflow_states,customer_workflow_readiness_subscriptions,data_upstreams,deployment_openapi_snapshots,deployment_rollback_operations,deployment_route_policy_snapshots,deployment_runtime_environment_owners,deployments,environment_gitops_runtime_effects,environment_management_overrides,exclusive_work_keys,github_deploy_branches,instance_runtime_config_receipts,instances,issue_ingest_tokens,managed_postgres_bindings,managed_postgres_cutovers,managed_postgres_databases,object_buckets,object_storage_s3_credentials,project_environment_approvals,project_environment_cleanup_jobs,project_environment_clone_configuration_ownership,project_environment_clone_materializations,project_environment_clone_operations,project_environment_config_versions,project_environment_promotions,project_environment_workload_heads,queue_bindings,runtime_instance_config_proofs,snapshot_runtime_config_receipts,snapshots,triggers IN SHARE MODE;
END $$;
-- +goose StatementEnd

-- +goose Down
LOCK TABLE project_environment_clone_environment_cleanups IN ACCESS EXCLUSIVE MODE;
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM project_environment_clone_environment_cleanups) THEN
  RAISE EXCEPTION 'environment cleanup evidence is still retained'
   USING ERRCODE='23514',CONSTRAINT='clone_environment_cleanup_down_no_evidence';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE project_environment_clone_environment_cleanups;
DROP FUNCTION lock_clone_environment_cleanup_scope_borrowers();
