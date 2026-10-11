from typing import Literal

ReuseWorkflowOperationArtifactXGregaleOperationExecutionKind = Literal["workflow"]

REUSE_WORKFLOW_OPERATION_ARTIFACT_X_GREGALE_OPERATION_EXECUTION_KIND_VALUES: set[
    ReuseWorkflowOperationArtifactXGregaleOperationExecutionKind
] = {
    "workflow",
}


def check_reuse_workflow_operation_artifact_x_gregale_operation_execution_kind(
    value: str,
) -> ReuseWorkflowOperationArtifactXGregaleOperationExecutionKind:
    if value in REUSE_WORKFLOW_OPERATION_ARTIFACT_X_GREGALE_OPERATION_EXECUTION_KIND_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {REUSE_WORKFLOW_OPERATION_ARTIFACT_X_GREGALE_OPERATION_EXECUTION_KIND_VALUES!r}"
    )
