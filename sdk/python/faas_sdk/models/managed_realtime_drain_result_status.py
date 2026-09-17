from typing import Literal

ManagedRealtimeDrainResultStatus = Literal["closed", "failed", "gone", "would_close"]

MANAGED_REALTIME_DRAIN_RESULT_STATUS_VALUES: set[ManagedRealtimeDrainResultStatus] = {
    "closed",
    "failed",
    "gone",
    "would_close",
}


def check_managed_realtime_drain_result_status(value: str) -> ManagedRealtimeDrainResultStatus:
    if value in MANAGED_REALTIME_DRAIN_RESULT_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {MANAGED_REALTIME_DRAIN_RESULT_STATUS_VALUES!r}")
