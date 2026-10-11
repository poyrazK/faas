from typing import Literal

ListPlatformTenantSelfWorkflowPerformanceInstancesDimension = Literal[
    "blocked_time", "blocker", "state", "state_time", "verification_owner", "verification_wait"
]

LIST_PLATFORM_TENANT_SELF_WORKFLOW_PERFORMANCE_INSTANCES_DIMENSION_VALUES: set[
    ListPlatformTenantSelfWorkflowPerformanceInstancesDimension
] = {
    "blocked_time",
    "blocker",
    "state",
    "state_time",
    "verification_owner",
    "verification_wait",
}


def check_list_platform_tenant_self_workflow_performance_instances_dimension(
    value: str,
) -> ListPlatformTenantSelfWorkflowPerformanceInstancesDimension:
    if value in LIST_PLATFORM_TENANT_SELF_WORKFLOW_PERFORMANCE_INSTANCES_DIMENSION_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {LIST_PLATFORM_TENANT_SELF_WORKFLOW_PERFORMANCE_INSTANCES_DIMENSION_VALUES!r}"
    )
