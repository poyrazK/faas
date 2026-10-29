from typing import Literal

WorkflowRecoveryReviewCompletionNotificationStatus = Literal["no_receivers", "queued"]

WORKFLOW_RECOVERY_REVIEW_COMPLETION_NOTIFICATION_STATUS_VALUES: set[
    WorkflowRecoveryReviewCompletionNotificationStatus
] = {
    "no_receivers",
    "queued",
}


def check_workflow_recovery_review_completion_notification_status(
    value: str,
) -> WorkflowRecoveryReviewCompletionNotificationStatus:
    if value in WORKFLOW_RECOVERY_REVIEW_COMPLETION_NOTIFICATION_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {WORKFLOW_RECOVERY_REVIEW_COMPLETION_NOTIFICATION_STATUS_VALUES!r}"
    )
