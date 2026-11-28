from typing import Literal

OperationWorkflowPlaybookMatchKind = Literal[
    "blocker", "missing_dependency", "missing_milestone", "pending_verification", "requirement"
]

OPERATION_WORKFLOW_PLAYBOOK_MATCH_KIND_VALUES: set[OperationWorkflowPlaybookMatchKind] = {
    "blocker",
    "missing_dependency",
    "missing_milestone",
    "pending_verification",
    "requirement",
}


def check_operation_workflow_playbook_match_kind(value: str) -> OperationWorkflowPlaybookMatchKind:
    if value in OPERATION_WORKFLOW_PLAYBOOK_MATCH_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_PLAYBOOK_MATCH_KIND_VALUES!r}")
