from typing import Literal

SummarizePlatformTenantSelfWorkflowAttentionGroupBy = Literal[
    "blocker_code", "dependency_status", "owner", "required_outcome_code", "target_operation", "workflow"
]

SUMMARIZE_PLATFORM_TENANT_SELF_WORKFLOW_ATTENTION_GROUP_BY_VALUES: set[
    SummarizePlatformTenantSelfWorkflowAttentionGroupBy
] = {
    "blocker_code",
    "dependency_status",
    "owner",
    "required_outcome_code",
    "target_operation",
    "workflow",
}


def check_summarize_platform_tenant_self_workflow_attention_group_by(
    value: str,
) -> SummarizePlatformTenantSelfWorkflowAttentionGroupBy:
    if value in SUMMARIZE_PLATFORM_TENANT_SELF_WORKFLOW_ATTENTION_GROUP_BY_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {SUMMARIZE_PLATFORM_TENANT_SELF_WORKFLOW_ATTENTION_GROUP_BY_VALUES!r}"
    )
