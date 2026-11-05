from typing import Literal

OperationWorkflowPlaybookEvidenceCheckStatus = Literal["invalidated", "missing", "satisfied", "stale", "unavailable"]

OPERATION_WORKFLOW_PLAYBOOK_EVIDENCE_CHECK_STATUS_VALUES: set[OperationWorkflowPlaybookEvidenceCheckStatus] = {
    "invalidated",
    "missing",
    "satisfied",
    "stale",
    "unavailable",
}


def check_operation_workflow_playbook_evidence_check_status(value: str) -> OperationWorkflowPlaybookEvidenceCheckStatus:
    if value in OPERATION_WORKFLOW_PLAYBOOK_EVIDENCE_CHECK_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_PLAYBOOK_EVIDENCE_CHECK_STATUS_VALUES!r}"
    )
