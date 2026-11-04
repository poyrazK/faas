from typing import Literal

OperationWorkflowPlaybookReviewRequestDecision = Literal["continue", "replace", "retire"]

OPERATION_WORKFLOW_PLAYBOOK_REVIEW_REQUEST_DECISION_VALUES: set[OperationWorkflowPlaybookReviewRequestDecision] = {
    "continue",
    "replace",
    "retire",
}


def check_operation_workflow_playbook_review_request_decision(
    value: str,
) -> OperationWorkflowPlaybookReviewRequestDecision:
    if value in OPERATION_WORKFLOW_PLAYBOOK_REVIEW_REQUEST_DECISION_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_PLAYBOOK_REVIEW_REQUEST_DECISION_VALUES!r}"
    )
