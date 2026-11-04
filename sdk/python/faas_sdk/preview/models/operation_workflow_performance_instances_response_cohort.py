from typing import Literal

OperationWorkflowPerformanceInstancesResponseCohort = Literal["completed", "ongoing"]

OPERATION_WORKFLOW_PERFORMANCE_INSTANCES_RESPONSE_COHORT_VALUES: set[
    OperationWorkflowPerformanceInstancesResponseCohort
] = {
    "completed",
    "ongoing",
}


def check_operation_workflow_performance_instances_response_cohort(
    value: str,
) -> OperationWorkflowPerformanceInstancesResponseCohort:
    if value in OPERATION_WORKFLOW_PERFORMANCE_INSTANCES_RESPONSE_COHORT_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_PERFORMANCE_INSTANCES_RESPONSE_COHORT_VALUES!r}"
    )
