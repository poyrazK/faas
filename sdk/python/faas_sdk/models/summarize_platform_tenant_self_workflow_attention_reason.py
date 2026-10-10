from typing import Literal

SummarizePlatformTenantSelfWorkflowAttentionReason = Literal["blocked", "dependency", "overdue", "stale"]

SUMMARIZE_PLATFORM_TENANT_SELF_WORKFLOW_ATTENTION_REASON_VALUES: set[
    SummarizePlatformTenantSelfWorkflowAttentionReason
] = {
    "blocked",
    "dependency",
    "overdue",
    "stale",
}


def check_summarize_platform_tenant_self_workflow_attention_reason(
    value: str,
) -> SummarizePlatformTenantSelfWorkflowAttentionReason:
    if value in SUMMARIZE_PLATFORM_TENANT_SELF_WORKFLOW_ATTENTION_REASON_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {SUMMARIZE_PLATFORM_TENANT_SELF_WORKFLOW_ATTENTION_REASON_VALUES!r}"
    )
