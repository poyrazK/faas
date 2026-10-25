from typing import Literal

OperationWorkflowObligationFulfillmentReviewActionKind = Literal[
    "coordinator_completion", "follow_up", "response_check_in"
]

OPERATION_WORKFLOW_OBLIGATION_FULFILLMENT_REVIEW_ACTION_KIND_VALUES: set[
    OperationWorkflowObligationFulfillmentReviewActionKind
] = {
    "coordinator_completion",
    "follow_up",
    "response_check_in",
}


def check_operation_workflow_obligation_fulfillment_review_action_kind(
    value: str,
) -> OperationWorkflowObligationFulfillmentReviewActionKind:
    if value in OPERATION_WORKFLOW_OBLIGATION_FULFILLMENT_REVIEW_ACTION_KIND_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_OBLIGATION_FULFILLMENT_REVIEW_ACTION_KIND_VALUES!r}"
    )
