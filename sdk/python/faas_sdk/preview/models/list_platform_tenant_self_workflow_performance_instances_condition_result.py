from typing import Literal

ListPlatformTenantSelfWorkflowPerformanceInstancesConditionResult = Literal["fail", "pass", "unknown"]

LIST_PLATFORM_TENANT_SELF_WORKFLOW_PERFORMANCE_INSTANCES_CONDITION_RESULT_VALUES: set[
    ListPlatformTenantSelfWorkflowPerformanceInstancesConditionResult
] = {
    "fail",
    "pass",
    "unknown",
}


def check_list_platform_tenant_self_workflow_performance_instances_condition_result(
    value: str,
) -> ListPlatformTenantSelfWorkflowPerformanceInstancesConditionResult:
    if value in LIST_PLATFORM_TENANT_SELF_WORKFLOW_PERFORMANCE_INSTANCES_CONDITION_RESULT_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {LIST_PLATFORM_TENANT_SELF_WORKFLOW_PERFORMANCE_INSTANCES_CONDITION_RESULT_VALUES!r}"
    )
