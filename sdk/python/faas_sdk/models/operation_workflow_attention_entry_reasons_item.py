from typing import Literal

OperationWorkflowAttentionEntryReasonsItem = Literal["blocked", "dependency", "overdue", "stale"]

OPERATION_WORKFLOW_ATTENTION_ENTRY_REASONS_ITEM_VALUES: set[OperationWorkflowAttentionEntryReasonsItem] = {
    "blocked",
    "dependency",
    "overdue",
    "stale",
}


def check_operation_workflow_attention_entry_reasons_item(value: str) -> OperationWorkflowAttentionEntryReasonsItem:
    if value in OPERATION_WORKFLOW_ATTENTION_ENTRY_REASONS_ITEM_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_ATTENTION_ENTRY_REASONS_ITEM_VALUES!r}"
    )
