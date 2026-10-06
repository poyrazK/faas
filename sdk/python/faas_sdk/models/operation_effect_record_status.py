from typing import Literal

OperationEffectRecordStatus = Literal["dead", "failed", "in_flight", "pending", "recorded", "succeeded", "unavailable"]

OPERATION_EFFECT_RECORD_STATUS_VALUES: set[OperationEffectRecordStatus] = {
    "dead",
    "failed",
    "in_flight",
    "pending",
    "recorded",
    "succeeded",
    "unavailable",
}


def check_operation_effect_record_status(value: str) -> OperationEffectRecordStatus:
    if value in OPERATION_EFFECT_RECORD_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OPERATION_EFFECT_RECORD_STATUS_VALUES!r}")
