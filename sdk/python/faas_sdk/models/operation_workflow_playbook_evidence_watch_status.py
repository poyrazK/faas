from typing import Literal

OperationWorkflowPlaybookEvidenceWatchStatus = Literal["active", "stopped"]

OPERATION_WORKFLOW_PLAYBOOK_EVIDENCE_WATCH_STATUS_VALUES: set[OperationWorkflowPlaybookEvidenceWatchStatus] = {
    "active",
    "stopped",
}


def check_operation_workflow_playbook_evidence_watch_status(value: str) -> OperationWorkflowPlaybookEvidenceWatchStatus:
    if value in OPERATION_WORKFLOW_PLAYBOOK_EVIDENCE_WATCH_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_PLAYBOOK_EVIDENCE_WATCH_STATUS_VALUES!r}"
    )
