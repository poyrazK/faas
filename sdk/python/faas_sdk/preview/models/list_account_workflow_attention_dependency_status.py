from typing import Literal

ListAccountWorkflowAttentionDependencyStatus = Literal["outcome_mismatch", "unknown", "waiting"]

LIST_ACCOUNT_WORKFLOW_ATTENTION_DEPENDENCY_STATUS_VALUES: set[ListAccountWorkflowAttentionDependencyStatus] = {
    "outcome_mismatch",
    "unknown",
    "waiting",
}


def check_list_account_workflow_attention_dependency_status(value: str) -> ListAccountWorkflowAttentionDependencyStatus:
    if value in LIST_ACCOUNT_WORKFLOW_ATTENTION_DEPENDENCY_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {LIST_ACCOUNT_WORKFLOW_ATTENTION_DEPENDENCY_STATUS_VALUES!r}"
    )
