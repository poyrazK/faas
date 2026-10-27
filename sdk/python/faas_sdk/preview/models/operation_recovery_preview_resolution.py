from typing import Literal

OperationRecoveryPreviewResolution = Literal["cancelled", "failed", "safe_to_retry", "succeeded"]

OPERATION_RECOVERY_PREVIEW_RESOLUTION_VALUES: set[OperationRecoveryPreviewResolution] = {
    "cancelled",
    "failed",
    "safe_to_retry",
    "succeeded",
}


def check_operation_recovery_preview_resolution(value: str) -> OperationRecoveryPreviewResolution:
    if value in OPERATION_RECOVERY_PREVIEW_RESOLUTION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OPERATION_RECOVERY_PREVIEW_RESOLUTION_VALUES!r}")
