from typing import Literal

OperationWorkflowReviewResponseSourceHealthStatus = Literal["observed", "unavailable"]

OPERATION_WORKFLOW_REVIEW_RESPONSE_SOURCE_HEALTH_STATUS_VALUES: set[
    OperationWorkflowReviewResponseSourceHealthStatus
] = {
    "observed",
    "unavailable",
}


def check_operation_workflow_review_response_source_health_status(
    value: str,
) -> OperationWorkflowReviewResponseSourceHealthStatus:
    if value in OPERATION_WORKFLOW_REVIEW_RESPONSE_SOURCE_HEALTH_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_REVIEW_RESPONSE_SOURCE_HEALTH_STATUS_VALUES!r}"
    )
