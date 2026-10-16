from typing import Literal

ListPlatformTenantSelfWorkflowReadinessFollowUpsActionTrigger = Literal["automatic", "human", "suggested"]

LIST_PLATFORM_TENANT_SELF_WORKFLOW_READINESS_FOLLOW_UPS_ACTION_TRIGGER_VALUES: set[
    ListPlatformTenantSelfWorkflowReadinessFollowUpsActionTrigger
] = {
    "automatic",
    "human",
    "suggested",
}


def check_list_platform_tenant_self_workflow_readiness_follow_ups_action_trigger(
    value: str,
) -> ListPlatformTenantSelfWorkflowReadinessFollowUpsActionTrigger:
    if value in LIST_PLATFORM_TENANT_SELF_WORKFLOW_READINESS_FOLLOW_UPS_ACTION_TRIGGER_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {LIST_PLATFORM_TENANT_SELF_WORKFLOW_READINESS_FOLLOW_UPS_ACTION_TRIGGER_VALUES!r}"
    )
