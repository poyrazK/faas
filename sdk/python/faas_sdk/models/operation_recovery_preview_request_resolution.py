from typing import Literal

OperationRecoveryPreviewRequestResolution = Literal["cancelled", "failed", "safe_to_retry", "succeeded"]

OPERATION_RECOVERY_PREVIEW_REQUEST_RESOLUTION_VALUES: set[OperationRecoveryPreviewRequestResolution] = {
    "cancelled",
    "failed",
    "safe_to_retry",
    "succeeded",
}


def check_operation_recovery_preview_request_resolution(value: str) -> OperationRecoveryPreviewRequestResolution:
    if value in OPERATION_RECOVERY_PREVIEW_REQUEST_RESOLUTION_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_RECOVERY_PREVIEW_REQUEST_RESOLUTION_VALUES!r}"
    )
