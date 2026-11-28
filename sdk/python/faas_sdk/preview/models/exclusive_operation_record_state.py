from typing import Literal

ExclusiveOperationRecordState = Literal["cancelled", "completed", "failed", "pending", "running"]

EXCLUSIVE_OPERATION_RECORD_STATE_VALUES: set[ExclusiveOperationRecordState] = {
    "cancelled",
    "completed",
    "failed",
    "pending",
    "running",
}


def check_exclusive_operation_record_state(value: str) -> ExclusiveOperationRecordState:
    if value in EXCLUSIVE_OPERATION_RECORD_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EXCLUSIVE_OPERATION_RECORD_STATE_VALUES!r}")
