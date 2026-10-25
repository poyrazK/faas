from typing import Literal

OperationWorkflowPlaybookDependencyStateStatus = Literal["completed", "pending"]

OPERATION_WORKFLOW_PLAYBOOK_DEPENDENCY_STATE_STATUS_VALUES: set[OperationWorkflowPlaybookDependencyStateStatus] = {
    "completed",
    "pending",
}


def check_operation_workflow_playbook_dependency_state_status(
    value: str,
) -> OperationWorkflowPlaybookDependencyStateStatus:
    if value in OPERATION_WORKFLOW_PLAYBOOK_DEPENDENCY_STATE_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_PLAYBOOK_DEPENDENCY_STATE_STATUS_VALUES!r}"
    )
