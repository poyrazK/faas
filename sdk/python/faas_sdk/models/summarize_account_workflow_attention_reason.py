from typing import Literal

SummarizeAccountWorkflowAttentionReason = Literal["blocked", "dependency", "overdue", "stale"]

SUMMARIZE_ACCOUNT_WORKFLOW_ATTENTION_REASON_VALUES: set[SummarizeAccountWorkflowAttentionReason] = {
    "blocked",
    "dependency",
    "overdue",
    "stale",
}


def check_summarize_account_workflow_attention_reason(value: str) -> SummarizeAccountWorkflowAttentionReason:
    if value in SUMMARIZE_ACCOUNT_WORKFLOW_ATTENTION_REASON_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {SUMMARIZE_ACCOUNT_WORKFLOW_ATTENTION_REASON_VALUES!r}"
    )
