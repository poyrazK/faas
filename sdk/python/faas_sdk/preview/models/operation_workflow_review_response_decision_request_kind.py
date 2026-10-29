from typing import Literal

OperationWorkflowReviewResponseDecisionRequestKind = Literal["continue", "pause", "revise"]

OPERATION_WORKFLOW_REVIEW_RESPONSE_DECISION_REQUEST_KIND_VALUES: set[
    OperationWorkflowReviewResponseDecisionRequestKind
] = {
    "continue",
    "pause",
    "revise",
}


def check_operation_workflow_review_response_decision_request_kind(
    value: str,
) -> OperationWorkflowReviewResponseDecisionRequestKind:
    if value in OPERATION_WORKFLOW_REVIEW_RESPONSE_DECISION_REQUEST_KIND_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_REVIEW_RESPONSE_DECISION_REQUEST_KIND_VALUES!r}"
    )
