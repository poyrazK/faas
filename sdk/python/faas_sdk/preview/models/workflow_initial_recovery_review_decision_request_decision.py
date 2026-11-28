from typing import Literal

WorkflowInitialRecoveryReviewDecisionRequestDecision = Literal["cancel", "submit"]

WORKFLOW_INITIAL_RECOVERY_REVIEW_DECISION_REQUEST_DECISION_VALUES: set[
    WorkflowInitialRecoveryReviewDecisionRequestDecision
] = {
    "cancel",
    "submit",
}


def check_workflow_initial_recovery_review_decision_request_decision(
    value: str,
) -> WorkflowInitialRecoveryReviewDecisionRequestDecision:
    if value in WORKFLOW_INITIAL_RECOVERY_REVIEW_DECISION_REQUEST_DECISION_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {WORKFLOW_INITIAL_RECOVERY_REVIEW_DECISION_REQUEST_DECISION_VALUES!r}"
    )
