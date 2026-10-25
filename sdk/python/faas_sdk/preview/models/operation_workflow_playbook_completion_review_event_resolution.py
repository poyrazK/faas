from typing import Literal

OperationWorkflowPlaybookCompletionReviewEventResolution = Literal[
    "alternative_accepted", "expected_outcome_achieved", "follow_up_canceled"
]

OPERATION_WORKFLOW_PLAYBOOK_COMPLETION_REVIEW_EVENT_RESOLUTION_VALUES: set[
    OperationWorkflowPlaybookCompletionReviewEventResolution
] = {
    "alternative_accepted",
    "expected_outcome_achieved",
    "follow_up_canceled",
}


def check_operation_workflow_playbook_completion_review_event_resolution(
    value: str,
) -> OperationWorkflowPlaybookCompletionReviewEventResolution:
    if value in OPERATION_WORKFLOW_PLAYBOOK_COMPLETION_REVIEW_EVENT_RESOLUTION_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_PLAYBOOK_COMPLETION_REVIEW_EVENT_RESOLUTION_VALUES!r}"
    )
