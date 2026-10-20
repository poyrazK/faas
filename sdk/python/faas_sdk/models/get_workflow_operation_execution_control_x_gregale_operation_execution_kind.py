from typing import Literal

GetWorkflowOperationExecutionControlXGregaleOperationExecutionKind = Literal["workflow"]

GET_WORKFLOW_OPERATION_EXECUTION_CONTROL_X_GREGALE_OPERATION_EXECUTION_KIND_VALUES: set[
    GetWorkflowOperationExecutionControlXGregaleOperationExecutionKind
] = {
    "workflow",
}


def check_get_workflow_operation_execution_control_x_gregale_operation_execution_kind(
    value: str,
) -> GetWorkflowOperationExecutionControlXGregaleOperationExecutionKind:
    if value in GET_WORKFLOW_OPERATION_EXECUTION_CONTROL_X_GREGALE_OPERATION_EXECUTION_KIND_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {GET_WORKFLOW_OPERATION_EXECUTION_CONTROL_X_GREGALE_OPERATION_EXECUTION_KIND_VALUES!r}"
    )
