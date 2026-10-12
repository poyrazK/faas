from typing import Literal

ListAccountWorkflowPerformanceInstancesDimension = Literal[
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

LIST_ACCOUNT_WORKFLOW_PERFORMANCE_INSTANCES_DIMENSION_VALUES: set[ListAccountWorkflowPerformanceInstancesDimension] = {
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


def check_list_account_workflow_performance_instances_dimension(
    value: str,
) -> ListAccountWorkflowPerformanceInstancesDimension:
    if value in LIST_ACCOUNT_WORKFLOW_PERFORMANCE_INSTANCES_DIMENSION_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {LIST_ACCOUNT_WORKFLOW_PERFORMANCE_INSTANCES_DIMENSION_VALUES!r}"
    )
