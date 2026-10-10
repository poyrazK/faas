from typing import Literal

ListPlatformTenantSelfWorkflowPerformanceInstancesCohort = Literal["completed", "ongoing"]

LIST_PLATFORM_TENANT_SELF_WORKFLOW_PERFORMANCE_INSTANCES_COHORT_VALUES: set[
    ListPlatformTenantSelfWorkflowPerformanceInstancesCohort
] = {
    "completed",
    "ongoing",
}


def check_list_platform_tenant_self_workflow_performance_instances_cohort(
    value: str,
) -> ListPlatformTenantSelfWorkflowPerformanceInstancesCohort:
    if value in LIST_PLATFORM_TENANT_SELF_WORKFLOW_PERFORMANCE_INSTANCES_COHORT_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {LIST_PLATFORM_TENANT_SELF_WORKFLOW_PERFORMANCE_INSTANCES_COHORT_VALUES!r}"
    )
