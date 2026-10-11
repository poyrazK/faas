from typing import Literal

ListAccountWorkflowPerformanceInstancesDimension = Literal[
    "blocked_time", "blocker", "state", "state_time", "verification_owner", "verification_wait"
]

LIST_ACCOUNT_WORKFLOW_PERFORMANCE_INSTANCES_DIMENSION_VALUES: set[ListAccountWorkflowPerformanceInstancesDimension] = {
    "blocked_time",
    "blocker",
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
