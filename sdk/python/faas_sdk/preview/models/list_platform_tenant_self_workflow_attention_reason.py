from typing import Literal

ListPlatformTenantSelfWorkflowAttentionReason = Literal[
    "awaiting_verification",
    "blocked",
    "dependency",
    "escalated",
    "follow_up_overdue",
    "overdue",
    "sla_at_risk",
    "sla_breached",
    "stale",
    "unacknowledged",
]

LIST_PLATFORM_TENANT_SELF_WORKFLOW_ATTENTION_REASON_VALUES: set[ListPlatformTenantSelfWorkflowAttentionReason] = {
    "awaiting_verification",
    "blocked",
    "dependency",
    "escalated",
    "follow_up_overdue",
    "overdue",
    "sla_at_risk",
    "sla_breached",
    "stale",
    "unacknowledged",
}


def check_list_platform_tenant_self_workflow_attention_reason(
    value: str,
) -> ListPlatformTenantSelfWorkflowAttentionReason:
    if value in LIST_PLATFORM_TENANT_SELF_WORKFLOW_ATTENTION_REASON_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {LIST_PLATFORM_TENANT_SELF_WORKFLOW_ATTENTION_REASON_VALUES!r}"
    )
