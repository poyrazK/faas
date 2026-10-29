from typing import Literal

SummarizeAccountWorkflowAttentionReason = Literal[
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

SUMMARIZE_ACCOUNT_WORKFLOW_ATTENTION_REASON_VALUES: set[SummarizeAccountWorkflowAttentionReason] = {
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


def check_summarize_account_workflow_attention_reason(value: str) -> SummarizeAccountWorkflowAttentionReason:
    if value in SUMMARIZE_ACCOUNT_WORKFLOW_ATTENTION_REASON_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {SUMMARIZE_ACCOUNT_WORKFLOW_ATTENTION_REASON_VALUES!r}"
    )
