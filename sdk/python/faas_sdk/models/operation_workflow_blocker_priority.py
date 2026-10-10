from typing import Literal

OperationWorkflowBlockerPriority = Literal["high", "low", "normal", "urgent"]

OPERATION_WORKFLOW_BLOCKER_PRIORITY_VALUES: set[OperationWorkflowBlockerPriority] = {
    "high",
    "low",
    "normal",
    "urgent",
}


def check_operation_workflow_blocker_priority(value: str) -> OperationWorkflowBlockerPriority:
    if value in OPERATION_WORKFLOW_BLOCKER_PRIORITY_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_BLOCKER_PRIORITY_VALUES!r}")
