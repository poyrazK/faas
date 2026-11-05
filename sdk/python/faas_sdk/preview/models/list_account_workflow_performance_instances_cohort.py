from typing import Literal

ListAccountWorkflowPerformanceInstancesCohort = Literal["completed", "ongoing"]

LIST_ACCOUNT_WORKFLOW_PERFORMANCE_INSTANCES_COHORT_VALUES: set[ListAccountWorkflowPerformanceInstancesCohort] = {
    "completed",
    "ongoing",
}


def check_list_account_workflow_performance_instances_cohort(
    value: str,
) -> ListAccountWorkflowPerformanceInstancesCohort:
    if value in LIST_ACCOUNT_WORKFLOW_PERFORMANCE_INSTANCES_COHORT_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {LIST_ACCOUNT_WORKFLOW_PERFORMANCE_INSTANCES_COHORT_VALUES!r}"
    )
