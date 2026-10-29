from typing import Literal

ListPlatformTenantSelfWorkflowAttentionPriority = Literal["high", "low", "normal", "urgent"]

LIST_PLATFORM_TENANT_SELF_WORKFLOW_ATTENTION_PRIORITY_VALUES: set[ListPlatformTenantSelfWorkflowAttentionPriority] = {
    "high",
    "low",
    "normal",
    "urgent",
}


def check_list_platform_tenant_self_workflow_attention_priority(
    value: str,
) -> ListPlatformTenantSelfWorkflowAttentionPriority:
    if value in LIST_PLATFORM_TENANT_SELF_WORKFLOW_ATTENTION_PRIORITY_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {LIST_PLATFORM_TENANT_SELF_WORKFLOW_ATTENTION_PRIORITY_VALUES!r}"
    )
