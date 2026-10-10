from typing import Literal

SummarizePlatformTenantSelfWorkflowAttentionSort = Literal["deadline", "updated_at"]

SUMMARIZE_PLATFORM_TENANT_SELF_WORKFLOW_ATTENTION_SORT_VALUES: set[SummarizePlatformTenantSelfWorkflowAttentionSort] = {
    "deadline",
    "updated_at",
}


def check_summarize_platform_tenant_self_workflow_attention_sort(
    value: str,
) -> SummarizePlatformTenantSelfWorkflowAttentionSort:
    if value in SUMMARIZE_PLATFORM_TENANT_SELF_WORKFLOW_ATTENTION_SORT_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {SUMMARIZE_PLATFORM_TENANT_SELF_WORKFLOW_ATTENTION_SORT_VALUES!r}"
    )
