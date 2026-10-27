from typing import Literal

WorkflowRecoveryReviewActionStatusOutcome = Literal["cancelled", "failed", "pending", "running", "succeeded", "unknown"]

WORKFLOW_RECOVERY_REVIEW_ACTION_STATUS_OUTCOME_VALUES: set[WorkflowRecoveryReviewActionStatusOutcome] = {
    "cancelled",
    "failed",
    "pending",
    "running",
    "succeeded",
    "unknown",
}


def check_workflow_recovery_review_action_status_outcome(value: str) -> WorkflowRecoveryReviewActionStatusOutcome:
    if value in WORKFLOW_RECOVERY_REVIEW_ACTION_STATUS_OUTCOME_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {WORKFLOW_RECOVERY_REVIEW_ACTION_STATUS_OUTCOME_VALUES!r}"
    )
