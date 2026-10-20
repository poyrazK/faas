from typing import Literal

OperationWorkflowPlaybookStepAssignmentStatus = Literal["completed", "pending"]

OPERATION_WORKFLOW_PLAYBOOK_STEP_ASSIGNMENT_STATUS_VALUES: set[OperationWorkflowPlaybookStepAssignmentStatus] = {
    "completed",
    "pending",
}


def check_operation_workflow_playbook_step_assignment_status(
    value: str,
) -> OperationWorkflowPlaybookStepAssignmentStatus:
    if value in OPERATION_WORKFLOW_PLAYBOOK_STEP_ASSIGNMENT_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_PLAYBOOK_STEP_ASSIGNMENT_STATUS_VALUES!r}"
    )
