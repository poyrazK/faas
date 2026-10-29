from typing import Literal

WorkflowRecoveryReviewDecisionDecision = Literal["approved", "dismissed"]

WORKFLOW_RECOVERY_REVIEW_DECISION_DECISION_VALUES: set[WorkflowRecoveryReviewDecisionDecision] = {
    "approved",
    "dismissed",
}


def check_workflow_recovery_review_decision_decision(value: str) -> WorkflowRecoveryReviewDecisionDecision:
    if value in WORKFLOW_RECOVERY_REVIEW_DECISION_DECISION_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {WORKFLOW_RECOVERY_REVIEW_DECISION_DECISION_VALUES!r}"
    )
