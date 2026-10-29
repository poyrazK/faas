from typing import Literal

OperationRecoveryDecisionState = Literal["accepted", "cancelled", "failed", "succeeded"]

OPERATION_RECOVERY_DECISION_STATE_VALUES: set[OperationRecoveryDecisionState] = {
    "accepted",
    "cancelled",
    "failed",
    "succeeded",
}


def check_operation_recovery_decision_state(value: str) -> OperationRecoveryDecisionState:
    if value in OPERATION_RECOVERY_DECISION_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OPERATION_RECOVERY_DECISION_STATE_VALUES!r}")
