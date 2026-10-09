from typing import Literal

SummarizeAccountWorkflowAttentionGroupBy = Literal[
    "blocker_code", "customer", "dependency_status", "required_outcome_code", "target_operation", "workflow"
]

SUMMARIZE_ACCOUNT_WORKFLOW_ATTENTION_GROUP_BY_VALUES: set[SummarizeAccountWorkflowAttentionGroupBy] = {
    "blocker_code",
    "customer",
    "dependency_status",
    "required_outcome_code",
    "target_operation",
    "workflow",
}


def check_summarize_account_workflow_attention_group_by(value: str) -> SummarizeAccountWorkflowAttentionGroupBy:
    if value in SUMMARIZE_ACCOUNT_WORKFLOW_ATTENTION_GROUP_BY_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {SUMMARIZE_ACCOUNT_WORKFLOW_ATTENTION_GROUP_BY_VALUES!r}"
    )
