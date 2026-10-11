from typing import Literal

OperationBusinessEffectStatus = Literal["confirmed", "failed", "pending"]

OPERATION_BUSINESS_EFFECT_STATUS_VALUES: set[OperationBusinessEffectStatus] = {
    "confirmed",
    "failed",
    "pending",
}


def check_operation_business_effect_status(value: str) -> OperationBusinessEffectStatus:
    if value in OPERATION_BUSINESS_EFFECT_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OPERATION_BUSINESS_EFFECT_STATUS_VALUES!r}")
