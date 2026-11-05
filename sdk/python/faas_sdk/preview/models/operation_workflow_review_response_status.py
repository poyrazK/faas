from typing import Literal

OperationWorkflowReviewResponseStatus = Literal["active", "resolved"]

OPERATION_WORKFLOW_REVIEW_RESPONSE_STATUS_VALUES: set[OperationWorkflowReviewResponseStatus] = {
    "active",
    "resolved",
}


def check_operation_workflow_review_response_status(value: str) -> OperationWorkflowReviewResponseStatus:
    if value in OPERATION_WORKFLOW_REVIEW_RESPONSE_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_REVIEW_RESPONSE_STATUS_VALUES!r}")
