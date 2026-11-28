from typing import Literal

OperationWorkflowReviewResponseHealthStatus = Literal["blocked", "clear", "review_required", "unavailable"]

OPERATION_WORKFLOW_REVIEW_RESPONSE_HEALTH_STATUS_VALUES: set[OperationWorkflowReviewResponseHealthStatus] = {
    "blocked",
    "clear",
    "review_required",
    "unavailable",
}


def check_operation_workflow_review_response_health_status(value: str) -> OperationWorkflowReviewResponseHealthStatus:
    if value in OPERATION_WORKFLOW_REVIEW_RESPONSE_HEALTH_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_REVIEW_RESPONSE_HEALTH_STATUS_VALUES!r}"
    )
