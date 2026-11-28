from typing import Literal

OperationWorkflowRevisionChangeKind = Literal[
    "blocker_added",
    "blocker_removed",
    "blocker_updated",
    "contract_changed",
    "decision_changed",
    "path_changed",
    "path_selected",
    "state_changed",
]

OPERATION_WORKFLOW_REVISION_CHANGE_KIND_VALUES: set[OperationWorkflowRevisionChangeKind] = {
    "blocker_added",
    "blocker_removed",
    "blocker_updated",
    "contract_changed",
    "decision_changed",
    "path_changed",
    "path_selected",
    "state_changed",
}


def check_operation_workflow_revision_change_kind(value: str) -> OperationWorkflowRevisionChangeKind:
    if value in OPERATION_WORKFLOW_REVISION_CHANGE_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_REVISION_CHANGE_KIND_VALUES!r}")
