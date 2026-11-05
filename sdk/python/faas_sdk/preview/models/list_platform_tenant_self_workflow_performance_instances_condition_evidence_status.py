from typing import Literal

ListPlatformTenantSelfWorkflowPerformanceInstancesConditionEvidenceStatus = Literal[
    "no_budget", "overdue", "within_budget"
]

LIST_PLATFORM_TENANT_SELF_WORKFLOW_PERFORMANCE_INSTANCES_CONDITION_EVIDENCE_STATUS_VALUES: set[
    ListPlatformTenantSelfWorkflowPerformanceInstancesConditionEvidenceStatus
] = {
    "no_budget",
    "overdue",
    "within_budget",
}


def check_list_platform_tenant_self_workflow_performance_instances_condition_evidence_status(
    value: str,
) -> ListPlatformTenantSelfWorkflowPerformanceInstancesConditionEvidenceStatus:
    if value in LIST_PLATFORM_TENANT_SELF_WORKFLOW_PERFORMANCE_INSTANCES_CONDITION_EVIDENCE_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {LIST_PLATFORM_TENANT_SELF_WORKFLOW_PERFORMANCE_INSTANCES_CONDITION_EVIDENCE_STATUS_VALUES!r}"
    )
