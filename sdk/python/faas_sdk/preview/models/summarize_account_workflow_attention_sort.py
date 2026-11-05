from typing import Literal

SummarizeAccountWorkflowAttentionSort = Literal["deadline", "updated_at"]

SUMMARIZE_ACCOUNT_WORKFLOW_ATTENTION_SORT_VALUES: set[SummarizeAccountWorkflowAttentionSort] = {
    "deadline",
    "updated_at",
}


def check_summarize_account_workflow_attention_sort(value: str) -> SummarizeAccountWorkflowAttentionSort:
    if value in SUMMARIZE_ACCOUNT_WORKFLOW_ATTENTION_SORT_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SUMMARIZE_ACCOUNT_WORKFLOW_ATTENTION_SORT_VALUES!r}")
