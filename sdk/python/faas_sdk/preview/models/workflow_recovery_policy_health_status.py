from typing import Literal

WorkflowRecoveryPolicyHealthStatus = Literal["blocked", "healthy", "inactive", "paused", "stalled", "unknown"]

WORKFLOW_RECOVERY_POLICY_HEALTH_STATUS_VALUES: set[WorkflowRecoveryPolicyHealthStatus] = {
    "blocked",
    "healthy",
    "inactive",
    "paused",
    "stalled",
    "unknown",
}


def check_workflow_recovery_policy_health_status(value: str) -> WorkflowRecoveryPolicyHealthStatus:
    if value in WORKFLOW_RECOVERY_POLICY_HEALTH_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {WORKFLOW_RECOVERY_POLICY_HEALTH_STATUS_VALUES!r}")
