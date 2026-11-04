from typing import Literal

OperationWorkflowReviewResponseDecisionKind = Literal["continue", "pause", "revise"]

OPERATION_WORKFLOW_REVIEW_RESPONSE_DECISION_KIND_VALUES: set[OperationWorkflowReviewResponseDecisionKind] = {
    "continue",
    "pause",
    "revise",
}


def check_operation_workflow_review_response_decision_kind(value: str) -> OperationWorkflowReviewResponseDecisionKind:
    if value in OPERATION_WORKFLOW_REVIEW_RESPONSE_DECISION_KIND_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_REVIEW_RESPONSE_DECISION_KIND_VALUES!r}"
    )
