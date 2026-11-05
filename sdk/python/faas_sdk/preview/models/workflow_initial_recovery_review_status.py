from typing import Literal

WorkflowInitialRecoveryReviewStatus = Literal["cancelled", "expired", "prepared", "stale", "submitted"]

WORKFLOW_INITIAL_RECOVERY_REVIEW_STATUS_VALUES: set[WorkflowInitialRecoveryReviewStatus] = {
    "cancelled",
    "expired",
    "prepared",
    "stale",
    "submitted",
}


def check_workflow_initial_recovery_review_status(value: str) -> WorkflowInitialRecoveryReviewStatus:
    if value in WORKFLOW_INITIAL_RECOVERY_REVIEW_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {WORKFLOW_INITIAL_RECOVERY_REVIEW_STATUS_VALUES!r}")
