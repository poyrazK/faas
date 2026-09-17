from typing import Literal

ManagedRealtimeDrainResponseStatus = Literal["completed", "partial", "running"]

MANAGED_REALTIME_DRAIN_RESPONSE_STATUS_VALUES: set[ManagedRealtimeDrainResponseStatus] = {
    "completed",
    "partial",
    "running",
}


def check_managed_realtime_drain_response_status(value: str) -> ManagedRealtimeDrainResponseStatus:
    if value in MANAGED_REALTIME_DRAIN_RESPONSE_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {MANAGED_REALTIME_DRAIN_RESPONSE_STATUS_VALUES!r}")
