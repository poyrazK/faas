from typing import Literal

OperationWorkflowReviewResponseChangeStatus = Literal["active", "resolved"]

OPERATION_WORKFLOW_REVIEW_RESPONSE_CHANGE_STATUS_VALUES: set[OperationWorkflowReviewResponseChangeStatus] = {
    "active",
    "resolved",
}


def check_operation_workflow_review_response_change_status(value: str) -> OperationWorkflowReviewResponseChangeStatus:
    if value in OPERATION_WORKFLOW_REVIEW_RESPONSE_CHANGE_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_REVIEW_RESPONSE_CHANGE_STATUS_VALUES!r}"
    )
