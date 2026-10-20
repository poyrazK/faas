-- filename: 20261020110814263_clone_borrowers_release_freezes.sql

-- ADR-20261019071733 follow-up. An automatic-rollback freeze names the
-- deployment that was rolled back (release_freezes.source_deployment_id, no
-- foreign key). Environment-clone cleanup must not delete a clone-owned
-- deployment a freeze still names, so cleanup locks release_freezes with the
-- other soft deployment borrowers and ReadCloneDeploymentSoftBorrowers counts
-- it. TestPgCloneDeploymentBorrowers failed on main because the column was not
-- inventoried.
-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION lock_clone_deployment_cleanup_borrower_writers() RETURNS void
LANGUAGE plpgsql AS $$ BEGIN
 LOCK TABLE deployments IN SHARE ROW EXCLUSIVE MODE;
 LOCK TABLE deployment_runtime_upgrade_acceptances,deployment_runtime_upgrade_baselines,deployment_runtime_upgrade_cutovers,environment_workload_serving_queue_acks,environment_workload_serving_routes,project_environment_clone_migration_adoptions,project_environment_clone_migration_resumes,project_environment_clone_wake_dispatches,route_probe_observations,runtime_upgrade_gateway_drains,runtime_upgrade_gateway_receipts,workflow_schedule_occurrences,app_issues,debug_regression_observations,deployment_audit,issue_events,issue_releases,issue_resolutions,log_events,mirror_invocation_results,org_activity,project_environment_clone_workloads,project_environment_promotion_workloads,request_audit_events,request_telemetry,upload_commit_outcomes,upload_sessions IN SHARE MODE;
 -- Retained PostgreSQL receipts and blocked plan members also borrow deployments.
 LOCK TABLE managed_postgres_cutover_drains,managed_postgres_cutover_drain_plan_members IN SHARE MODE;
 -- Automatic-rollback freezes name the rolled-back deployment.
 LOCK TABLE release_freezes IN SHARE MODE;
END $$;
-- +goose StatementEnd

-- +goose Down
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION lock_clone_deployment_cleanup_borrower_writers() RETURNS void
LANGUAGE plpgsql AS $$ BEGIN
 LOCK TABLE deployments IN SHARE ROW EXCLUSIVE MODE;
 LOCK TABLE deployment_runtime_upgrade_acceptances,deployment_runtime_upgrade_baselines,deployment_runtime_upgrade_cutovers,environment_workload_serving_queue_acks,environment_workload_serving_routes,project_environment_clone_migration_adoptions,project_environment_clone_migration_resumes,project_environment_clone_wake_dispatches,route_probe_observations,runtime_upgrade_gateway_drains,runtime_upgrade_gateway_receipts,workflow_schedule_occurrences,app_issues,debug_regression_observations,deployment_audit,issue_events,issue_releases,issue_resolutions,log_events,mirror_invocation_results,org_activity,project_environment_clone_workloads,project_environment_promotion_workloads,request_audit_events,request_telemetry,upload_commit_outcomes,upload_sessions IN SHARE MODE;
 -- Retained PostgreSQL receipts and blocked plan members also borrow deployments.
 LOCK TABLE managed_postgres_cutover_drains,managed_postgres_cutover_drain_plan_members IN SHARE MODE;
END $$;
-- +goose StatementEnd
