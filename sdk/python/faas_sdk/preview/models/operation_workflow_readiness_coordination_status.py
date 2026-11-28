from typing import Literal

OperationWorkflowReadinessCoordinationStatus = Literal["acknowledged", "read", "unread"]

OPERATION_WORKFLOW_READINESS_COORDINATION_STATUS_VALUES: set[OperationWorkflowReadinessCoordinationStatus] = {
    "acknowledged",
    "read",
    "unread",
}


def check_operation_workflow_readiness_coordination_status(value: str) -> OperationWorkflowReadinessCoordinationStatus:
    if value in OPERATION_WORKFLOW_READINESS_COORDINATION_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_READINESS_COORDINATION_STATUS_VALUES!r}"
    )
