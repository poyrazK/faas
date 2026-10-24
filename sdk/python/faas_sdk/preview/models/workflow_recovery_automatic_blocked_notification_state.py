from typing import Literal

WorkflowRecoveryAutomaticBlockedNotificationState = Literal["blocked", "recovered"]

WORKFLOW_RECOVERY_AUTOMATIC_BLOCKED_NOTIFICATION_STATE_VALUES: set[
    WorkflowRecoveryAutomaticBlockedNotificationState
] = {
    "blocked",
    "recovered",
}


def check_workflow_recovery_automatic_blocked_notification_state(
    value: str,
) -> WorkflowRecoveryAutomaticBlockedNotificationState:
    if value in WORKFLOW_RECOVERY_AUTOMATIC_BLOCKED_NOTIFICATION_STATE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {WORKFLOW_RECOVERY_AUTOMATIC_BLOCKED_NOTIFICATION_STATE_VALUES!r}"
    )
