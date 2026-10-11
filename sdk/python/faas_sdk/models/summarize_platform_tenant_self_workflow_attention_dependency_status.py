from typing import Literal

SummarizePlatformTenantSelfWorkflowAttentionDependencyStatus = Literal["outcome_mismatch", "unknown", "waiting"]

SUMMARIZE_PLATFORM_TENANT_SELF_WORKFLOW_ATTENTION_DEPENDENCY_STATUS_VALUES: set[
    SummarizePlatformTenantSelfWorkflowAttentionDependencyStatus
] = {
    "outcome_mismatch",
    "unknown",
    "waiting",
}


def check_summarize_platform_tenant_self_workflow_attention_dependency_status(
    value: str,
) -> SummarizePlatformTenantSelfWorkflowAttentionDependencyStatus:
    if value in SUMMARIZE_PLATFORM_TENANT_SELF_WORKFLOW_ATTENTION_DEPENDENCY_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {SUMMARIZE_PLATFORM_TENANT_SELF_WORKFLOW_ATTENTION_DEPENDENCY_STATUS_VALUES!r}"
    )
