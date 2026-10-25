from typing import Literal

WorkflowRecoveryReviewDecisionRequestDecision = Literal["approved", "dismissed"]

WORKFLOW_RECOVERY_REVIEW_DECISION_REQUEST_DECISION_VALUES: set[WorkflowRecoveryReviewDecisionRequestDecision] = {
    "approved",
    "dismissed",
}


def check_workflow_recovery_review_decision_request_decision(
    value: str,
) -> WorkflowRecoveryReviewDecisionRequestDecision:
    if value in WORKFLOW_RECOVERY_REVIEW_DECISION_REQUEST_DECISION_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {WORKFLOW_RECOVERY_REVIEW_DECISION_REQUEST_DECISION_VALUES!r}"
    )
