from typing import Literal

ListPlatformTenantSelfWorkflowPerformanceInstancesGapStatus = Literal[
    "contract_changed", "expectation_changed", "new_expectation", "report_lost", "still_missing"
]

LIST_PLATFORM_TENANT_SELF_WORKFLOW_PERFORMANCE_INSTANCES_GAP_STATUS_VALUES: set[
    ListPlatformTenantSelfWorkflowPerformanceInstancesGapStatus
] = {
    "contract_changed",
    "expectation_changed",
    "new_expectation",
    "report_lost",
    "still_missing",
}


def check_list_platform_tenant_self_workflow_performance_instances_gap_status(
    value: str,
) -> ListPlatformTenantSelfWorkflowPerformanceInstancesGapStatus:
    if value in LIST_PLATFORM_TENANT_SELF_WORKFLOW_PERFORMANCE_INSTANCES_GAP_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {LIST_PLATFORM_TENANT_SELF_WORKFLOW_PERFORMANCE_INSTANCES_GAP_STATUS_VALUES!r}"
    )
