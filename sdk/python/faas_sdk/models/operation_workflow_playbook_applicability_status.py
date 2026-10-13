from typing import Literal

OperationWorkflowPlaybookApplicabilityStatus = Literal[
    "applicable",
    "contract_changed",
    "definition_unavailable",
    "evidence_incomplete",
    "evidence_unavailable",
    "guidance_changed",
    "terminal_reported",
    "trigger_absent",
]

OPERATION_WORKFLOW_PLAYBOOK_APPLICABILITY_STATUS_VALUES: set[OperationWorkflowPlaybookApplicabilityStatus] = {
    "applicable",
    "contract_changed",
    "definition_unavailable",
    "evidence_incomplete",
    "evidence_unavailable",
    "guidance_changed",
    "terminal_reported",
    "trigger_absent",
}


def check_operation_workflow_playbook_applicability_status(value: str) -> OperationWorkflowPlaybookApplicabilityStatus:
    if value in OPERATION_WORKFLOW_PLAYBOOK_APPLICABILITY_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_PLAYBOOK_APPLICABILITY_STATUS_VALUES!r}"
    )
