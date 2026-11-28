from typing import Literal

ListPlatformTenantSelfWorkflowReadinessFollowUpsImpactPriority = Literal["critical", "high", "normal"]

LIST_PLATFORM_TENANT_SELF_WORKFLOW_READINESS_FOLLOW_UPS_IMPACT_PRIORITY_VALUES: set[
    ListPlatformTenantSelfWorkflowReadinessFollowUpsImpactPriority
] = {
    "critical",
    "high",
    "normal",
}


def check_list_platform_tenant_self_workflow_readiness_follow_ups_impact_priority(
    value: str,
) -> ListPlatformTenantSelfWorkflowReadinessFollowUpsImpactPriority:
    if value in LIST_PLATFORM_TENANT_SELF_WORKFLOW_READINESS_FOLLOW_UPS_IMPACT_PRIORITY_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {LIST_PLATFORM_TENANT_SELF_WORKFLOW_READINESS_FOLLOW_UPS_IMPACT_PRIORITY_VALUES!r}"
    )
