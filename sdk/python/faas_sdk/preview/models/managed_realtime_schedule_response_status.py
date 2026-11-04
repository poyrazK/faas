from typing import Literal

ManagedRealtimeScheduleResponseStatus = Literal["canceled", "failed", "paused", "pending", "published", "skipped"]

MANAGED_REALTIME_SCHEDULE_RESPONSE_STATUS_VALUES: set[ManagedRealtimeScheduleResponseStatus] = {
    "canceled",
    "failed",
    "paused",
    "pending",
    "published",
    "skipped",
}


def check_managed_realtime_schedule_response_status(value: str) -> ManagedRealtimeScheduleResponseStatus:
    if value in MANAGED_REALTIME_SCHEDULE_RESPONSE_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {MANAGED_REALTIME_SCHEDULE_RESPONSE_STATUS_VALUES!r}")
