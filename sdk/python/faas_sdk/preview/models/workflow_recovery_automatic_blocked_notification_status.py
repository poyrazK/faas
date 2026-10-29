from typing import Literal

WorkflowRecoveryAutomaticBlockedNotificationStatus = Literal["no_receivers", "queued"]

WORKFLOW_RECOVERY_AUTOMATIC_BLOCKED_NOTIFICATION_STATUS_VALUES: set[
    WorkflowRecoveryAutomaticBlockedNotificationStatus
] = {
    "no_receivers",
    "queued",
}


def check_workflow_recovery_automatic_blocked_notification_status(
    value: str,
) -> WorkflowRecoveryAutomaticBlockedNotificationStatus:
    if value in WORKFLOW_RECOVERY_AUTOMATIC_BLOCKED_NOTIFICATION_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {WORKFLOW_RECOVERY_AUTOMATIC_BLOCKED_NOTIFICATION_STATUS_VALUES!r}"
    )
