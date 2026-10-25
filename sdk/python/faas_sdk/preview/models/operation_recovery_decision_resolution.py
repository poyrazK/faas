from typing import Literal

OperationRecoveryDecisionResolution = Literal["cancelled", "failed", "safe_to_retry", "succeeded"]

OPERATION_RECOVERY_DECISION_RESOLUTION_VALUES: set[OperationRecoveryDecisionResolution] = {
    "cancelled",
    "failed",
    "safe_to_retry",
    "succeeded",
}


def check_operation_recovery_decision_resolution(value: str) -> OperationRecoveryDecisionResolution:
    if value in OPERATION_RECOVERY_DECISION_RESOLUTION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OPERATION_RECOVERY_DECISION_RESOLUTION_VALUES!r}")
