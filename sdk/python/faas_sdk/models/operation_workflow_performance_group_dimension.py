from typing import Literal

OperationWorkflowPerformanceGroupDimension = Literal[
    "blocked_time", "blocker", "state", "state_time", "verification_owner", "verification_wait"
]

OPERATION_WORKFLOW_PERFORMANCE_GROUP_DIMENSION_VALUES: set[OperationWorkflowPerformanceGroupDimension] = {
    "blocked_time",
    "blocker",
    "state",
    "state_time",
    "verification_owner",
    "verification_wait",
}


def check_operation_workflow_performance_group_dimension(value: str) -> OperationWorkflowPerformanceGroupDimension:
    if value in OPERATION_WORKFLOW_PERFORMANCE_GROUP_DIMENSION_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_PERFORMANCE_GROUP_DIMENSION_VALUES!r}"
    )
