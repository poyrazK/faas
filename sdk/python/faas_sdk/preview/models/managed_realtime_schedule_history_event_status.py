from typing import Literal

ManagedRealtimeScheduleHistoryEventStatus = Literal["canceled", "failed", "paused", "pending", "published", "skipped"]

MANAGED_REALTIME_SCHEDULE_HISTORY_EVENT_STATUS_VALUES: set[ManagedRealtimeScheduleHistoryEventStatus] = {
    "canceled",
    "failed",
    "paused",
    "pending",
    "published",
    "skipped",
}


def check_managed_realtime_schedule_history_event_status(value: str) -> ManagedRealtimeScheduleHistoryEventStatus:
    if value in MANAGED_REALTIME_SCHEDULE_HISTORY_EVENT_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {MANAGED_REALTIME_SCHEDULE_HISTORY_EVENT_STATUS_VALUES!r}"
    )
