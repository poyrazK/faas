from typing import Literal

ListAccountWorkflowAttentionReason = Literal["blocked", "dependency", "overdue", "stale"]

LIST_ACCOUNT_WORKFLOW_ATTENTION_REASON_VALUES: set[ListAccountWorkflowAttentionReason] = {
    "blocked",
    "dependency",
    "overdue",
    "stale",
}


def check_list_account_workflow_attention_reason(value: str) -> ListAccountWorkflowAttentionReason:
    if value in LIST_ACCOUNT_WORKFLOW_ATTENTION_REASON_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {LIST_ACCOUNT_WORKFLOW_ATTENTION_REASON_VALUES!r}")
