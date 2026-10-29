from typing import Literal

CapabilityStatusUnavailableReason = Literal["plan_not_entitled", "runtime_unavailable"]

CAPABILITY_STATUS_UNAVAILABLE_REASON_VALUES: set[CapabilityStatusUnavailableReason] = {
    "plan_not_entitled",
    "runtime_unavailable",
}


def check_capability_status_unavailable_reason(value: str) -> CapabilityStatusUnavailableReason:
    if value in CAPABILITY_STATUS_UNAVAILABLE_REASON_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {CAPABILITY_STATUS_UNAVAILABLE_REASON_VALUES!r}")
