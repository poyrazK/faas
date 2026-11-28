from typing import Literal

OperationWorkflowReviewResponseRequestStatus = Literal["active", "resolved"]

OPERATION_WORKFLOW_REVIEW_RESPONSE_REQUEST_STATUS_VALUES: set[OperationWorkflowReviewResponseRequestStatus] = {
    "active",
    "resolved",
}


def check_operation_workflow_review_response_request_status(value: str) -> OperationWorkflowReviewResponseRequestStatus:
    if value in OPERATION_WORKFLOW_REVIEW_RESPONSE_REQUEST_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_REVIEW_RESPONSE_REQUEST_STATUS_VALUES!r}"
    )
