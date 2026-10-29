from typing import Literal

ListPlatformTenantSelfWorkflowPerformanceInstancesKind = Literal["decision", "path"]

LIST_PLATFORM_TENANT_SELF_WORKFLOW_PERFORMANCE_INSTANCES_KIND_VALUES: set[
    ListPlatformTenantSelfWorkflowPerformanceInstancesKind
] = {
    "decision",
    "path",
}


def check_list_platform_tenant_self_workflow_performance_instances_kind(
    value: str,
) -> ListPlatformTenantSelfWorkflowPerformanceInstancesKind:
    if value in LIST_PLATFORM_TENANT_SELF_WORKFLOW_PERFORMANCE_INSTANCES_KIND_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {LIST_PLATFORM_TENANT_SELF_WORKFLOW_PERFORMANCE_INSTANCES_KIND_VALUES!r}"
    )
