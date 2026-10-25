from typing import Literal

OperationWorkflowPrerequisiteRecoveryReason = Literal["awaiting_verification", "expired", "invalidated"]

OPERATION_WORKFLOW_PREREQUISITE_RECOVERY_REASON_VALUES: set[OperationWorkflowPrerequisiteRecoveryReason] = {
    "awaiting_verification",
    "expired",
    "invalidated",
}


def check_operation_workflow_prerequisite_recovery_reason(value: str) -> OperationWorkflowPrerequisiteRecoveryReason:
    if value in OPERATION_WORKFLOW_PREREQUISITE_RECOVERY_REASON_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_PREREQUISITE_RECOVERY_REASON_VALUES!r}"
    )
