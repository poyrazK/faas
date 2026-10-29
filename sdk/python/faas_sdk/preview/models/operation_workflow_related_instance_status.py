from typing import Literal

OperationWorkflowRelatedInstanceStatus = Literal["outcome_mismatch", "satisfied", "terminal", "unknown", "waiting"]

OPERATION_WORKFLOW_RELATED_INSTANCE_STATUS_VALUES: set[OperationWorkflowRelatedInstanceStatus] = {
    "outcome_mismatch",
    "satisfied",
    "terminal",
    "unknown",
    "waiting",
}


def check_operation_workflow_related_instance_status(value: str) -> OperationWorkflowRelatedInstanceStatus:
    if value in OPERATION_WORKFLOW_RELATED_INSTANCE_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_RELATED_INSTANCE_STATUS_VALUES!r}"
    )
