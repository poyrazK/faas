from typing import Literal

SummarizePlatformTenantSelfWorkflowAttentionReason = Literal[
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

SUMMARIZE_PLATFORM_TENANT_SELF_WORKFLOW_ATTENTION_REASON_VALUES: set[
    SummarizePlatformTenantSelfWorkflowAttentionReason
] = {
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


def check_summarize_platform_tenant_self_workflow_attention_reason(
    value: str,
) -> SummarizePlatformTenantSelfWorkflowAttentionReason:
    if value in SUMMARIZE_PLATFORM_TENANT_SELF_WORKFLOW_ATTENTION_REASON_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {SUMMARIZE_PLATFORM_TENANT_SELF_WORKFLOW_ATTENTION_REASON_VALUES!r}"
    )
