from typing import Literal

OperationWorkflowPlaybookReviewImpactPriority = Literal["critical", "high", "normal"]

OPERATION_WORKFLOW_PLAYBOOK_REVIEW_IMPACT_PRIORITY_VALUES: set[OperationWorkflowPlaybookReviewImpactPriority] = {
    "critical",
    "high",
    "normal",
}


def check_operation_workflow_playbook_review_impact_priority(
    value: str,
) -> OperationWorkflowPlaybookReviewImpactPriority:
    if value in OPERATION_WORKFLOW_PLAYBOOK_REVIEW_IMPACT_PRIORITY_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_PLAYBOOK_REVIEW_IMPACT_PRIORITY_VALUES!r}"
    )
