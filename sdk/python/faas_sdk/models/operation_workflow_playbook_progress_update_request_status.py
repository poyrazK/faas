from typing import Literal

OperationWorkflowPlaybookProgressUpdateRequestStatus = Literal["completed", "pending"]

OPERATION_WORKFLOW_PLAYBOOK_PROGRESS_UPDATE_REQUEST_STATUS_VALUES: set[
    OperationWorkflowPlaybookProgressUpdateRequestStatus
] = {
    "completed",
    "pending",
}


def check_operation_workflow_playbook_progress_update_request_status(
    value: str,
) -> OperationWorkflowPlaybookProgressUpdateRequestStatus:
    if value in OPERATION_WORKFLOW_PLAYBOOK_PROGRESS_UPDATE_REQUEST_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_PLAYBOOK_PROGRESS_UPDATE_REQUEST_STATUS_VALUES!r}"
    )
