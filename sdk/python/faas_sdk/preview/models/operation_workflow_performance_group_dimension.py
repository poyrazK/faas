from typing import Literal

OperationWorkflowPerformanceGroupDimension = Literal[
    "active_condition_blocked",
    "active_condition_elapsed",
    "blocked_time",
    "blocker",
    "condition_blocked",
    "condition_completion",
    "condition_gap_owner",
    "condition_gap_queue",
    "condition_missing_owner",
    "condition_refresh_age",
    "condition_refresh_blocked",
    "condition_refresh_owner",
    "decision_blocked",
    "decision_completion",
    "state",
    "state_time",
    "verification_owner",
    "verification_wait",
]

OPERATION_WORKFLOW_PERFORMANCE_GROUP_DIMENSION_VALUES: set[OperationWorkflowPerformanceGroupDimension] = {
    "active_condition_blocked",
    "active_condition_elapsed",
    "blocked_time",
    "blocker",
    "condition_blocked",
    "condition_completion",
    "condition_gap_owner",
    "condition_gap_queue",
    "condition_missing_owner",
    "condition_refresh_age",
    "condition_refresh_blocked",
    "condition_refresh_owner",
    "decision_blocked",
    "decision_completion",
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
