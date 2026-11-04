from typing import Literal

OperationWorkflowConditionChangeStatus = Literal["changed", "newly_reported", "unreported"]

OPERATION_WORKFLOW_CONDITION_CHANGE_STATUS_VALUES: set[OperationWorkflowConditionChangeStatus] = {
    "changed",
    "newly_reported",
    "unreported",
}


def check_operation_workflow_condition_change_status(value: str) -> OperationWorkflowConditionChangeStatus:
    if value in OPERATION_WORKFLOW_CONDITION_CHANGE_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_CONDITION_CHANGE_STATUS_VALUES!r}"
    )
