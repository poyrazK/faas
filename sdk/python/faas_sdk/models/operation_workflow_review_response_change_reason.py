from typing import Literal

OperationWorkflowReviewResponseChangeReason = Literal["check_in_overdue", "human_update"]

OPERATION_WORKFLOW_REVIEW_RESPONSE_CHANGE_REASON_VALUES: set[OperationWorkflowReviewResponseChangeReason] = {
    "check_in_overdue",
    "human_update",
}


def check_operation_workflow_review_response_change_reason(value: str) -> OperationWorkflowReviewResponseChangeReason:
    if value in OPERATION_WORKFLOW_REVIEW_RESPONSE_CHANGE_REASON_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_REVIEW_RESPONSE_CHANGE_REASON_VALUES!r}"
    )
