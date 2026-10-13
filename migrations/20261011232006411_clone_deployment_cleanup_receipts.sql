-- +goose Up
-- ADR-590: retain original native proof across atomic artifact removal.
CREATE TABLE IF NOT EXISTS project_environment_clone_deployment_cleanups (
 operation_id uuid PRIMARY KEY REFERENCES project_environment_clone_workload_cleanups(operation_id) ON DELETE RESTRICT,
 environment_id uuid NOT NULL,
 workload_cleanup_sha256 text NOT NULL CHECK(workload_cleanup_sha256 ~ '^[a-f0-9]{64}$'),
 native_plan_sha256 text NOT NULL CHECK(native_plan_sha256 ~ '^[a-f0-9]{64}$'),
 native_observation_sha256 text NOT NULL CHECK(native_observation_sha256 ~ '^[a-f0-9]{64}$'),
 removed_deployments bigint NOT NULL CHECK(removed_deployments>=0),
 cleaned_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION lock_clone_deployment_cleanup_borrower_writers() RETURNS void
LANGUAGE plpgsql AS $$ BEGIN
 LOCK TABLE deployments IN SHARE ROW EXCLUSIVE MODE;
 LOCK TABLE deployment_runtime_upgrade_acceptances,deployment_runtime_upgrade_baselines,deployment_runtime_upgrade_cutovers,environment_workload_serving_queue_acks,environment_workload_serving_routes,project_environment_clone_migration_adoptions,project_environment_clone_migration_resumes,project_environment_clone_wake_dispatches,route_probe_observations,runtime_upgrade_gateway_drains,runtime_upgrade_gateway_receipts,workflow_schedule_occurrences,app_issues,debug_regression_observations,deployment_audit,issue_events,issue_releases,issue_resolutions,log_events,mirror_invocation_results,org_activity,project_environment_clone_workloads,project_environment_promotion_workloads,request_audit_events,request_telemetry,upload_commit_outcomes,upload_sessions IN SHARE MODE;
END $$;
-- +goose StatementEnd

-- +goose Down
LOCK TABLE project_environment_clone_deployment_cleanups IN ACCESS EXCLUSIVE MODE;
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM project_environment_clone_deployment_cleanups) THEN
  RAISE EXCEPTION 'deployment cleanup evidence is still retained'
   USING ERRCODE='23514',CONSTRAINT='clone_deployment_cleanup_down_no_evidence';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE project_environment_clone_deployment_cleanups;
DROP FUNCTION lock_clone_deployment_cleanup_borrower_writers();
