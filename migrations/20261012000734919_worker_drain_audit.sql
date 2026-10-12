-- +goose Up
ALTER TABLE deployment_audit DROP CONSTRAINT IF EXISTS deployment_audit_kind_chk;
ALTER TABLE deployment_audit ADD CONSTRAINT deployment_audit_kind_chk CHECK (kind IN ('deploy.created','deploy.source_ref','deploy.local_tarball','deploy.traffic_changed','deploy.health_probe_failed','deploy.health_recovered','deploy.rolled_back','deploy.removed','deploy.rollout_started','deploy.rollout_completed','deploy.rollout_aborted','deploy.canary_step_advanced','deploy.alert_rule_fired','deploy.scan_regressed','deploy.mcp_task_held','deploy.mcp_task_retry_requested','deploy.mcp_task_retry_started','deploy.mcp_task_retry_completed','deploy.mcp_task_recovered','deploy.mcp_task_hold_cancelled','deploy.worker_drain_started','deploy.worker_drain_completed'));
CREATE INDEX deployment_audit_worker_drain_idx ON deployment_audit (deployment_id,id DESC)
    WHERE kind IN ('deploy.worker_drain_started','deploy.worker_drain_completed');

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM deployment_audit WHERE kind IN ('deploy.worker_drain_started','deploy.worker_drain_completed')) THEN
        RAISE EXCEPTION 'retain worker drain audit events before rollback';
    END IF;
END $$;
-- +goose StatementEnd
DROP INDEX deployment_audit_worker_drain_idx;
ALTER TABLE deployment_audit DROP CONSTRAINT IF EXISTS deployment_audit_kind_chk;
ALTER TABLE deployment_audit ADD CONSTRAINT deployment_audit_kind_chk CHECK (kind IN ('deploy.created','deploy.source_ref','deploy.local_tarball','deploy.traffic_changed','deploy.health_probe_failed','deploy.health_recovered','deploy.rolled_back','deploy.removed','deploy.rollout_started','deploy.rollout_completed','deploy.rollout_aborted','deploy.canary_step_advanced','deploy.alert_rule_fired','deploy.scan_regressed','deploy.mcp_task_held','deploy.mcp_task_retry_requested','deploy.mcp_task_retry_started','deploy.mcp_task_retry_completed','deploy.mcp_task_recovered','deploy.mcp_task_hold_cancelled'));
