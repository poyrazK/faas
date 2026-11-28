from typing import Literal

WorkflowRecoveryPollingNotificationStatus = Literal["no_receivers", "queued"]

WORKFLOW_RECOVERY_POLLING_NOTIFICATION_STATUS_VALUES: set[WorkflowRecoveryPollingNotificationStatus] = {
    "no_receivers",
    "queued",
}


def check_workflow_recovery_polling_notification_status(value: str) -> WorkflowRecoveryPollingNotificationStatus:
    if value in WORKFLOW_RECOVERY_POLLING_NOTIFICATION_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {WORKFLOW_RECOVERY_POLLING_NOTIFICATION_STATUS_VALUES!r}"
    )
