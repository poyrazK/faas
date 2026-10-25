from typing import Literal

WorkflowInitialRecoveryReviewEventStatus = Literal["cancelled", "expired", "prepared", "stale", "submitted"]

WORKFLOW_INITIAL_RECOVERY_REVIEW_EVENT_STATUS_VALUES: set[WorkflowInitialRecoveryReviewEventStatus] = {
    "cancelled",
    "expired",
    "prepared",
    "stale",
    "submitted",
}


def check_workflow_initial_recovery_review_event_status(value: str) -> WorkflowInitialRecoveryReviewEventStatus:
    if value in WORKFLOW_INITIAL_RECOVERY_REVIEW_EVENT_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {WORKFLOW_INITIAL_RECOVERY_REVIEW_EVENT_STATUS_VALUES!r}"
    )
