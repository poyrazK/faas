from typing import Literal

SummarizePlatformTenantSelfWorkflowAttentionPriority = Literal["high", "low", "normal", "urgent"]

SUMMARIZE_PLATFORM_TENANT_SELF_WORKFLOW_ATTENTION_PRIORITY_VALUES: set[
    SummarizePlatformTenantSelfWorkflowAttentionPriority
] = {
    "high",
    "low",
    "normal",
    "urgent",
}


def check_summarize_platform_tenant_self_workflow_attention_priority(
    value: str,
) -> SummarizePlatformTenantSelfWorkflowAttentionPriority:
    if value in SUMMARIZE_PLATFORM_TENANT_SELF_WORKFLOW_ATTENTION_PRIORITY_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {SUMMARIZE_PLATFORM_TENANT_SELF_WORKFLOW_ATTENTION_PRIORITY_VALUES!r}"
    )
