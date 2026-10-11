from typing import Literal

ReuseWorkflowOperationUploadXGregaleOperationExecutionKind = Literal["workflow"]

REUSE_WORKFLOW_OPERATION_UPLOAD_X_GREGALE_OPERATION_EXECUTION_KIND_VALUES: set[
    ReuseWorkflowOperationUploadXGregaleOperationExecutionKind
] = {
    "workflow",
}


def check_reuse_workflow_operation_upload_x_gregale_operation_execution_kind(
    value: str,
) -> ReuseWorkflowOperationUploadXGregaleOperationExecutionKind:
    if value in REUSE_WORKFLOW_OPERATION_UPLOAD_X_GREGALE_OPERATION_EXECUTION_KIND_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {REUSE_WORKFLOW_OPERATION_UPLOAD_X_GREGALE_OPERATION_EXECUTION_KIND_VALUES!r}"
    )
