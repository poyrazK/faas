from typing import Literal

OperationWorkflowPlaybookStepAssignmentDependencyReviewStatus = Literal[
    "awaiting", "confirmed", "redo_requested", "superseded"
]

OPERATION_WORKFLOW_PLAYBOOK_STEP_ASSIGNMENT_DEPENDENCY_REVIEW_STATUS_VALUES: set[
    OperationWorkflowPlaybookStepAssignmentDependencyReviewStatus
] = {
    "awaiting",
    "confirmed",
    "redo_requested",
    "superseded",
}


def check_operation_workflow_playbook_step_assignment_dependency_review_status(
    value: str,
) -> OperationWorkflowPlaybookStepAssignmentDependencyReviewStatus:
    if value in OPERATION_WORKFLOW_PLAYBOOK_STEP_ASSIGNMENT_DEPENDENCY_REVIEW_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_PLAYBOOK_STEP_ASSIGNMENT_DEPENDENCY_REVIEW_STATUS_VALUES!r}"
    )
