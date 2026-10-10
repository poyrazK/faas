from typing import Literal

OperationRecoveryInspectionState = Literal[
    "accepted", "cancelled", "failed", "requires_reconciliation", "running", "succeeded"
]

OPERATION_RECOVERY_INSPECTION_STATE_VALUES: set[OperationRecoveryInspectionState] = {
    "accepted",
    "cancelled",
    "failed",
    "requires_reconciliation",
    "running",
    "succeeded",
}


def check_operation_recovery_inspection_state(value: str) -> OperationRecoveryInspectionState:
    if value in OPERATION_RECOVERY_INSPECTION_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OPERATION_RECOVERY_INSPECTION_STATE_VALUES!r}")
