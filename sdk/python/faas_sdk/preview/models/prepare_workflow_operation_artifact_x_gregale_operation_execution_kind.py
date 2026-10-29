from typing import Literal

PrepareWorkflowOperationArtifactXGregaleOperationExecutionKind = Literal["workflow"]

PREPARE_WORKFLOW_OPERATION_ARTIFACT_X_GREGALE_OPERATION_EXECUTION_KIND_VALUES: set[
    PrepareWorkflowOperationArtifactXGregaleOperationExecutionKind
] = {
    "workflow",
}


def check_prepare_workflow_operation_artifact_x_gregale_operation_execution_kind(
    value: str,
) -> PrepareWorkflowOperationArtifactXGregaleOperationExecutionKind:
    if value in PREPARE_WORKFLOW_OPERATION_ARTIFACT_X_GREGALE_OPERATION_EXECUTION_KIND_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {PREPARE_WORKFLOW_OPERATION_ARTIFACT_X_GREGALE_OPERATION_EXECUTION_KIND_VALUES!r}"
    )
