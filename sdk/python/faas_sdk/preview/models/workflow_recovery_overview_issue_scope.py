from typing import Literal

WorkflowRecoveryOverviewIssueScope = Literal["automatic_policy", "automatic_queue", "manual_initial", "manual_retry"]

WORKFLOW_RECOVERY_OVERVIEW_ISSUE_SCOPE_VALUES: set[WorkflowRecoveryOverviewIssueScope] = {
    "automatic_policy",
    "automatic_queue",
    "manual_initial",
    "manual_retry",
}


def check_workflow_recovery_overview_issue_scope(value: str) -> WorkflowRecoveryOverviewIssueScope:
    if value in WORKFLOW_RECOVERY_OVERVIEW_ISSUE_SCOPE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {WORKFLOW_RECOVERY_OVERVIEW_ISSUE_SCOPE_VALUES!r}")
