from typing import Literal

ListPlatformTenantSelfWorkflowAttentionDependencyStatus = Literal["outcome_mismatch", "unknown", "waiting"]

LIST_PLATFORM_TENANT_SELF_WORKFLOW_ATTENTION_DEPENDENCY_STATUS_VALUES: set[
    ListPlatformTenantSelfWorkflowAttentionDependencyStatus
] = {
    "outcome_mismatch",
    "unknown",
    "waiting",
}


def check_list_platform_tenant_self_workflow_attention_dependency_status(
    value: str,
) -> ListPlatformTenantSelfWorkflowAttentionDependencyStatus:
    if value in LIST_PLATFORM_TENANT_SELF_WORKFLOW_ATTENTION_DEPENDENCY_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {LIST_PLATFORM_TENANT_SELF_WORKFLOW_ATTENTION_DEPENDENCY_STATUS_VALUES!r}"
    )
