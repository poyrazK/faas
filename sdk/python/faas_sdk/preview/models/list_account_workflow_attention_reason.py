from typing import Literal

ListAccountWorkflowAttentionReason = Literal[
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

LIST_ACCOUNT_WORKFLOW_ATTENTION_REASON_VALUES: set[ListAccountWorkflowAttentionReason] = {
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


def check_list_account_workflow_attention_reason(value: str) -> ListAccountWorkflowAttentionReason:
    if value in LIST_ACCOUNT_WORKFLOW_ATTENTION_REASON_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {LIST_ACCOUNT_WORKFLOW_ATTENTION_REASON_VALUES!r}")
