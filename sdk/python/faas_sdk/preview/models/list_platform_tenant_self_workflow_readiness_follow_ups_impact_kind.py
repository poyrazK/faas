from typing import Literal

ListPlatformTenantSelfWorkflowReadinessFollowUpsImpactKind = Literal["milestone", "outcome"]

LIST_PLATFORM_TENANT_SELF_WORKFLOW_READINESS_FOLLOW_UPS_IMPACT_KIND_VALUES: set[
    ListPlatformTenantSelfWorkflowReadinessFollowUpsImpactKind
] = {
    "milestone",
    "outcome",
}


def check_list_platform_tenant_self_workflow_readiness_follow_ups_impact_kind(
    value: str,
) -> ListPlatformTenantSelfWorkflowReadinessFollowUpsImpactKind:
    if value in LIST_PLATFORM_TENANT_SELF_WORKFLOW_READINESS_FOLLOW_UPS_IMPACT_KIND_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {LIST_PLATFORM_TENANT_SELF_WORKFLOW_READINESS_FOLLOW_UPS_IMPACT_KIND_VALUES!r}"
    )
