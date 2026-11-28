from typing import Literal

OperationWorkflowReadinessSnapshotVersion = Literal[1]

OPERATION_WORKFLOW_READINESS_SNAPSHOT_VERSION_VALUES: set[OperationWorkflowReadinessSnapshotVersion] = {
    1,
}


def check_operation_workflow_readiness_snapshot_version(value: int) -> OperationWorkflowReadinessSnapshotVersion:
    if value in OPERATION_WORKFLOW_READINESS_SNAPSHOT_VERSION_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_READINESS_SNAPSHOT_VERSION_VALUES!r}"
    )
