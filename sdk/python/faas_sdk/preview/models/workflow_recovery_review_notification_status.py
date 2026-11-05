from typing import Literal

WorkflowRecoveryReviewNotificationStatus = Literal["no_receivers", "queued", "suppressed"]

WORKFLOW_RECOVERY_REVIEW_NOTIFICATION_STATUS_VALUES: set[WorkflowRecoveryReviewNotificationStatus] = {
    "no_receivers",
    "queued",
    "suppressed",
}


def check_workflow_recovery_review_notification_status(value: str) -> WorkflowRecoveryReviewNotificationStatus:
    if value in WORKFLOW_RECOVERY_REVIEW_NOTIFICATION_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {WORKFLOW_RECOVERY_REVIEW_NOTIFICATION_STATUS_VALUES!r}"
    )
