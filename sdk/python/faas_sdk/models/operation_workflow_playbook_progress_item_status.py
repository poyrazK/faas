from typing import Literal

OperationWorkflowPlaybookProgressItemStatus = Literal["completed", "pending"]

OPERATION_WORKFLOW_PLAYBOOK_PROGRESS_ITEM_STATUS_VALUES: set[OperationWorkflowPlaybookProgressItemStatus] = {
    "completed",
    "pending",
}


def check_operation_workflow_playbook_progress_item_status(value: str) -> OperationWorkflowPlaybookProgressItemStatus:
    if value in OPERATION_WORKFLOW_PLAYBOOK_PROGRESS_ITEM_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_PLAYBOOK_PROGRESS_ITEM_STATUS_VALUES!r}"
    )
