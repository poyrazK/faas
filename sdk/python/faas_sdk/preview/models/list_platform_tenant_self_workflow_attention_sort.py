from typing import Literal

ListPlatformTenantSelfWorkflowAttentionSort = Literal["deadline", "updated_at"]

LIST_PLATFORM_TENANT_SELF_WORKFLOW_ATTENTION_SORT_VALUES: set[ListPlatformTenantSelfWorkflowAttentionSort] = {
    "deadline",
    "updated_at",
}


def check_list_platform_tenant_self_workflow_attention_sort(value: str) -> ListPlatformTenantSelfWorkflowAttentionSort:
    if value in LIST_PLATFORM_TENANT_SELF_WORKFLOW_ATTENTION_SORT_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {LIST_PLATFORM_TENANT_SELF_WORKFLOW_ATTENTION_SORT_VALUES!r}"
    )
