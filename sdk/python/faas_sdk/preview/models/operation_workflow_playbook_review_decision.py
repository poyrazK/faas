from typing import Literal

OperationWorkflowPlaybookReviewDecision = Literal["continue", "replace", "retire"]

OPERATION_WORKFLOW_PLAYBOOK_REVIEW_DECISION_VALUES: set[OperationWorkflowPlaybookReviewDecision] = {
    "continue",
    "replace",
    "retire",
}


def check_operation_workflow_playbook_review_decision(value: str) -> OperationWorkflowPlaybookReviewDecision:
    if value in OPERATION_WORKFLOW_PLAYBOOK_REVIEW_DECISION_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_PLAYBOOK_REVIEW_DECISION_VALUES!r}"
    )
