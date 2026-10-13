-- +goose Up
ALTER TABLE deployment_audit DROP CONSTRAINT deployment_audit_kind_chk;
ALTER TABLE deployment_audit ADD CONSTRAINT deployment_audit_kind_chk CHECK (kind IN ('deploy.created','deploy.source_ref','deploy.local_tarball','deploy.traffic_changed','deploy.health_probe_failed','deploy.health_recovered','deploy.rolled_back','deploy.removed','deploy.rollout_started','deploy.rollout_completed','deploy.rollout_aborted','deploy.canary_step_advanced','deploy.alert_rule_fired','deploy.scan_regressed','deploy.mcp_task_held','deploy.mcp_task_retry_requested','deploy.mcp_task_retry_started','deploy.mcp_task_retry_completed','deploy.mcp_task_recovered','deploy.mcp_task_hold_cancelled','deploy.worker_drain_started','deploy.worker_drain_completed','deploy.mcp_admission'));
CREATE UNIQUE INDEX deployment_audit_mcp_admission_id_idx ON deployment_audit ((data->'mcp_admission'->>'id')) WHERE kind='deploy.mcp_admission';

-- +goose Down
DROP INDEX deployment_audit_mcp_admission_id_idx;
DELETE FROM deployment_audit WHERE kind='deploy.mcp_admission';
ALTER TABLE deployment_audit DROP CONSTRAINT deployment_audit_kind_chk;
ALTER TABLE deployment_audit ADD CONSTRAINT deployment_audit_kind_chk CHECK (kind IN ('deploy.created','deploy.source_ref','deploy.local_tarball','deploy.traffic_changed','deploy.health_probe_failed','deploy.health_recovered','deploy.rolled_back','deploy.removed','deploy.rollout_started','deploy.rollout_completed','deploy.rollout_aborted','deploy.canary_step_advanced','deploy.alert_rule_fired','deploy.scan_regressed','deploy.mcp_task_held','deploy.mcp_task_retry_requested','deploy.mcp_task_retry_started','deploy.mcp_task_retry_completed','deploy.mcp_task_recovered','deploy.mcp_task_hold_cancelled','deploy.worker_drain_started','deploy.worker_drain_completed'));
