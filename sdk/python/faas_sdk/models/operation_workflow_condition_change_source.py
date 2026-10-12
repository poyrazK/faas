from typing import Literal

OperationWorkflowConditionChangeSource = Literal["path", "reason"]

OPERATION_WORKFLOW_CONDITION_CHANGE_SOURCE_VALUES: set[OperationWorkflowConditionChangeSource] = {
    "path",
    "reason",
}


def check_operation_workflow_condition_change_source(value: str) -> OperationWorkflowConditionChangeSource:
    if value in OPERATION_WORKFLOW_CONDITION_CHANGE_SOURCE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_CONDITION_CHANGE_SOURCE_VALUES!r}"
    )
