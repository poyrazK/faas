from typing import Literal

SummarizeAccountWorkflowAttentionDependencyStatus = Literal["outcome_mismatch", "unknown", "waiting"]

SUMMARIZE_ACCOUNT_WORKFLOW_ATTENTION_DEPENDENCY_STATUS_VALUES: set[
    SummarizeAccountWorkflowAttentionDependencyStatus
] = {
    "outcome_mismatch",
    "unknown",
    "waiting",
}


def check_summarize_account_workflow_attention_dependency_status(
    value: str,
) -> SummarizeAccountWorkflowAttentionDependencyStatus:
    if value in SUMMARIZE_ACCOUNT_WORKFLOW_ATTENTION_DEPENDENCY_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {SUMMARIZE_ACCOUNT_WORKFLOW_ATTENTION_DEPENDENCY_STATUS_VALUES!r}"
    )
