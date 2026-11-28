from typing import Literal

OperationRecoveryArtifactState = Literal["attached", "prepared"]

OPERATION_RECOVERY_ARTIFACT_STATE_VALUES: set[OperationRecoveryArtifactState] = {
    "attached",
    "prepared",
}


def check_operation_recovery_artifact_state(value: str) -> OperationRecoveryArtifactState:
    if value in OPERATION_RECOVERY_ARTIFACT_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OPERATION_RECOVERY_ARTIFACT_STATE_VALUES!r}")
