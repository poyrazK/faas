from typing import Literal

UploadWorkflowOperationArtifactXGregaleOperationExecutionKind = Literal["workflow"]

UPLOAD_WORKFLOW_OPERATION_ARTIFACT_X_GREGALE_OPERATION_EXECUTION_KIND_VALUES: set[
    UploadWorkflowOperationArtifactXGregaleOperationExecutionKind
] = {
    "workflow",
}


def check_upload_workflow_operation_artifact_x_gregale_operation_execution_kind(
    value: str,
) -> UploadWorkflowOperationArtifactXGregaleOperationExecutionKind:
    if value in UPLOAD_WORKFLOW_OPERATION_ARTIFACT_X_GREGALE_OPERATION_EXECUTION_KIND_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {UPLOAD_WORKFLOW_OPERATION_ARTIFACT_X_GREGALE_OPERATION_EXECUTION_KIND_VALUES!r}"
    )
