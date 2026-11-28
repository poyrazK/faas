from typing import Literal

OperationRecoveryRequestResolution = Literal["cancelled", "failed", "safe_to_retry", "succeeded"]

OPERATION_RECOVERY_REQUEST_RESOLUTION_VALUES: set[OperationRecoveryRequestResolution] = {
    "cancelled",
    "failed",
    "safe_to_retry",
    "succeeded",
}


def check_operation_recovery_request_resolution(value: str) -> OperationRecoveryRequestResolution:
    if value in OPERATION_RECOVERY_REQUEST_RESOLUTION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OPERATION_RECOVERY_REQUEST_RESOLUTION_VALUES!r}")
