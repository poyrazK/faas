from typing import Literal

OperationWorkflowPlaybookReviewImpactKind = Literal["milestone", "outcome"]

OPERATION_WORKFLOW_PLAYBOOK_REVIEW_IMPACT_KIND_VALUES: set[OperationWorkflowPlaybookReviewImpactKind] = {
    "milestone",
    "outcome",
}


def check_operation_workflow_playbook_review_impact_kind(value: str) -> OperationWorkflowPlaybookReviewImpactKind:
    if value in OPERATION_WORKFLOW_PLAYBOOK_REVIEW_IMPACT_KIND_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_PLAYBOOK_REVIEW_IMPACT_KIND_VALUES!r}"
    )
