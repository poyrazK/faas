from typing import Literal

OperationWorkflowAttentionEntryReasonsItem = Literal[
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

OPERATION_WORKFLOW_ATTENTION_ENTRY_REASONS_ITEM_VALUES: set[OperationWorkflowAttentionEntryReasonsItem] = {
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


def check_operation_workflow_attention_entry_reasons_item(value: str) -> OperationWorkflowAttentionEntryReasonsItem:
    if value in OPERATION_WORKFLOW_ATTENTION_ENTRY_REASONS_ITEM_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_ATTENTION_ENTRY_REASONS_ITEM_VALUES!r}"
    )
