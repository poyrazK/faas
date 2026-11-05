from typing import Literal

ListPlatformTenantSelfWorkflowReadinessFollowUpsDue = Literal["all", "overdue", "upcoming"]

LIST_PLATFORM_TENANT_SELF_WORKFLOW_READINESS_FOLLOW_UPS_DUE_VALUES: set[
    ListPlatformTenantSelfWorkflowReadinessFollowUpsDue
] = {
    "all",
    "overdue",
    "upcoming",
}


def check_list_platform_tenant_self_workflow_readiness_follow_ups_due(
    value: str,
) -> ListPlatformTenantSelfWorkflowReadinessFollowUpsDue:
    if value in LIST_PLATFORM_TENANT_SELF_WORKFLOW_READINESS_FOLLOW_UPS_DUE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {LIST_PLATFORM_TENANT_SELF_WORKFLOW_READINESS_FOLLOW_UPS_DUE_VALUES!r}"
    )
