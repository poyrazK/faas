from typing import Literal

WorkflowRecoveryHealthIncidentStatus = Literal["blocked", "healthy", "inactive", "paused", "stalled", "unknown"]

WORKFLOW_RECOVERY_HEALTH_INCIDENT_STATUS_VALUES: set[WorkflowRecoveryHealthIncidentStatus] = {
    "blocked",
    "healthy",
    "inactive",
    "paused",
    "stalled",
    "unknown",
}


def check_workflow_recovery_health_incident_status(value: str) -> WorkflowRecoveryHealthIncidentStatus:
    if value in WORKFLOW_RECOVERY_HEALTH_INCIDENT_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {WORKFLOW_RECOVERY_HEALTH_INCIDENT_STATUS_VALUES!r}")
