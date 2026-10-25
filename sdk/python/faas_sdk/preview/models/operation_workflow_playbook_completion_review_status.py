from typing import Literal

OperationWorkflowPlaybookCompletionReviewStatus = Literal["active", "expired", "stopped"]

OPERATION_WORKFLOW_PLAYBOOK_COMPLETION_REVIEW_STATUS_VALUES: set[OperationWorkflowPlaybookCompletionReviewStatus] = {
    "active",
    "expired",
    "stopped",
}


def check_operation_workflow_playbook_completion_review_status(
    value: str,
) -> OperationWorkflowPlaybookCompletionReviewStatus:
    if value in OPERATION_WORKFLOW_PLAYBOOK_COMPLETION_REVIEW_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_PLAYBOOK_COMPLETION_REVIEW_STATUS_VALUES!r}"
    )
