from typing import Literal

SummarizeAccountWorkflowAttentionPriority = Literal["high", "low", "normal", "urgent"]

SUMMARIZE_ACCOUNT_WORKFLOW_ATTENTION_PRIORITY_VALUES: set[SummarizeAccountWorkflowAttentionPriority] = {
    "high",
    "low",
    "normal",
    "urgent",
}


def check_summarize_account_workflow_attention_priority(value: str) -> SummarizeAccountWorkflowAttentionPriority:
    if value in SUMMARIZE_ACCOUNT_WORKFLOW_ATTENTION_PRIORITY_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {SUMMARIZE_ACCOUNT_WORKFLOW_ATTENTION_PRIORITY_VALUES!r}"
    )
