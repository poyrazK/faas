from typing import Literal

ListPlatformTenantSelfWorkflowReadinessFollowUpsView = Literal["follow_ups", "next_actions"]

LIST_PLATFORM_TENANT_SELF_WORKFLOW_READINESS_FOLLOW_UPS_VIEW_VALUES: set[
    ListPlatformTenantSelfWorkflowReadinessFollowUpsView
] = {
    "follow_ups",
    "next_actions",
}


def check_list_platform_tenant_self_workflow_readiness_follow_ups_view(
    value: str,
) -> ListPlatformTenantSelfWorkflowReadinessFollowUpsView:
    if value in LIST_PLATFORM_TENANT_SELF_WORKFLOW_READINESS_FOLLOW_UPS_VIEW_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {LIST_PLATFORM_TENANT_SELF_WORKFLOW_READINESS_FOLLOW_UPS_VIEW_VALUES!r}"
    )
