from typing import Literal

WorkflowRecoveryReviewStatusOutcome = Literal[
    "cancelled", "completed", "failed", "no_plan", "pending", "running", "succeeded", "unknown"
]

WORKFLOW_RECOVERY_REVIEW_STATUS_OUTCOME_VALUES: set[WorkflowRecoveryReviewStatusOutcome] = {
    "cancelled",
    "completed",
    "failed",
    "no_plan",
    "pending",
    "running",
    "succeeded",
    "unknown",
}


def check_workflow_recovery_review_status_outcome(value: str) -> WorkflowRecoveryReviewStatusOutcome:
    if value in WORKFLOW_RECOVERY_REVIEW_STATUS_OUTCOME_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {WORKFLOW_RECOVERY_REVIEW_STATUS_OUTCOME_VALUES!r}")
