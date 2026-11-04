from typing import Literal

OperationWorkflowPlaybookProgressDisposition = Literal["active", "replaced", "retired"]

OPERATION_WORKFLOW_PLAYBOOK_PROGRESS_DISPOSITION_VALUES: set[OperationWorkflowPlaybookProgressDisposition] = {
    "active",
    "replaced",
    "retired",
}


def check_operation_workflow_playbook_progress_disposition(value: str) -> OperationWorkflowPlaybookProgressDisposition:
    if value in OPERATION_WORKFLOW_PLAYBOOK_PROGRESS_DISPOSITION_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_PLAYBOOK_PROGRESS_DISPOSITION_VALUES!r}"
    )
