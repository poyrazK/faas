from typing import Literal

OperationWorkflowOutcomeLinkStatus = Literal[
    "history_incomplete", "linked", "outcome_unavailable", "timing_inconsistent"
]

OPERATION_WORKFLOW_OUTCOME_LINK_STATUS_VALUES: set[OperationWorkflowOutcomeLinkStatus] = {
    "history_incomplete",
    "linked",
    "outcome_unavailable",
    "timing_inconsistent",
}


def check_operation_workflow_outcome_link_status(value: str) -> OperationWorkflowOutcomeLinkStatus:
    if value in OPERATION_WORKFLOW_OUTCOME_LINK_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_OUTCOME_LINK_STATUS_VALUES!r}")
