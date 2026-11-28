from typing import Literal

DeploymentAuditResponseKind = Literal[
    "deploy.alert_rule_fired",
    "deploy.canary_step_advanced",
    "deploy.created",
    "deploy.health_probe_failed",
    "deploy.health_recovered",
    "deploy.local_tarball",
    "deploy.mcp_admission",
    "deploy.mcp_task_held",
    "deploy.mcp_task_hold_cancelled",
    "deploy.mcp_task_recovered",
    "deploy.mcp_task_retry_completed",
    "deploy.mcp_task_retry_requested",
    "deploy.mcp_task_retry_started",
    "deploy.recovered",
    "deploy.removed",
    "deploy.rolled_back",
    "deploy.rolled_forward",
    "deploy.rollout_aborted",
    "deploy.rollout_completed",
    "deploy.rollout_started",
    "deploy.scan_regressed",
    "deploy.source_ref",
    "deploy.stuck",
    "deploy.traffic_changed",
    "deploy.worker_drain_completed",
    "deploy.worker_drain_started",
]

DEPLOYMENT_AUDIT_RESPONSE_KIND_VALUES: set[DeploymentAuditResponseKind] = {
    "deploy.alert_rule_fired",
    "deploy.canary_step_advanced",
    "deploy.created",
    "deploy.health_probe_failed",
    "deploy.health_recovered",
    "deploy.local_tarball",
    "deploy.mcp_admission",
    "deploy.mcp_task_held",
    "deploy.mcp_task_hold_cancelled",
    "deploy.mcp_task_recovered",
    "deploy.mcp_task_retry_completed",
    "deploy.mcp_task_retry_requested",
    "deploy.mcp_task_retry_started",
    "deploy.recovered",
    "deploy.removed",
    "deploy.rolled_back",
    "deploy.rolled_forward",
    "deploy.rollout_aborted",
    "deploy.rollout_completed",
    "deploy.rollout_started",
    "deploy.scan_regressed",
    "deploy.source_ref",
    "deploy.stuck",
    "deploy.traffic_changed",
    "deploy.worker_drain_completed",
    "deploy.worker_drain_started",
}


def check_deployment_audit_response_kind(value: str) -> DeploymentAuditResponseKind:
    if value in DEPLOYMENT_AUDIT_RESPONSE_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DEPLOYMENT_AUDIT_RESPONSE_KIND_VALUES!r}")
