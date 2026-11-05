from typing import Literal

ListPlatformTenantSelfWorkflowReadinessFollowUpsSort = Literal["business_impact", "due"]

LIST_PLATFORM_TENANT_SELF_WORKFLOW_READINESS_FOLLOW_UPS_SORT_VALUES: set[
    ListPlatformTenantSelfWorkflowReadinessFollowUpsSort
] = {
    "business_impact",
    "due",
}


def check_list_platform_tenant_self_workflow_readiness_follow_ups_sort(
    value: str,
) -> ListPlatformTenantSelfWorkflowReadinessFollowUpsSort:
    if value in LIST_PLATFORM_TENANT_SELF_WORKFLOW_READINESS_FOLLOW_UPS_SORT_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {LIST_PLATFORM_TENANT_SELF_WORKFLOW_READINESS_FOLLOW_UPS_SORT_VALUES!r}"
    )
