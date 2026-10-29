from typing import Literal

OperationWorkflowReadinessCoordinationRequestStatus = Literal["acknowledged", "read", "unread"]

OPERATION_WORKFLOW_READINESS_COORDINATION_REQUEST_STATUS_VALUES: set[
    OperationWorkflowReadinessCoordinationRequestStatus
] = {
    "acknowledged",
    "read",
    "unread",
}


def check_operation_workflow_readiness_coordination_request_status(
    value: str,
) -> OperationWorkflowReadinessCoordinationRequestStatus:
    if value in OPERATION_WORKFLOW_READINESS_COORDINATION_REQUEST_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_READINESS_COORDINATION_REQUEST_STATUS_VALUES!r}"
    )
