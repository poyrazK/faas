from typing import Literal

ListAccountWorkflowAttentionPriority = Literal["high", "low", "normal", "urgent"]

LIST_ACCOUNT_WORKFLOW_ATTENTION_PRIORITY_VALUES: set[ListAccountWorkflowAttentionPriority] = {
    "high",
    "low",
    "normal",
    "urgent",
}


def check_list_account_workflow_attention_priority(value: str) -> ListAccountWorkflowAttentionPriority:
    if value in LIST_ACCOUNT_WORKFLOW_ATTENTION_PRIORITY_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {LIST_ACCOUNT_WORKFLOW_ATTENTION_PRIORITY_VALUES!r}")
