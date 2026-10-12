from typing import Literal

WorkflowRecoveryHealthNotificationStatus = Literal["no_receivers", "queued"]

WORKFLOW_RECOVERY_HEALTH_NOTIFICATION_STATUS_VALUES: set[WorkflowRecoveryHealthNotificationStatus] = {
    "no_receivers",
    "queued",
}


def check_workflow_recovery_health_notification_status(value: str) -> WorkflowRecoveryHealthNotificationStatus:
    if value in WORKFLOW_RECOVERY_HEALTH_NOTIFICATION_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {WORKFLOW_RECOVERY_HEALTH_NOTIFICATION_STATUS_VALUES!r}"
    )
