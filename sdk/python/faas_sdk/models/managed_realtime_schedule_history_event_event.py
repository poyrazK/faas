from typing import Literal

ManagedRealtimeScheduleHistoryEventEvent = Literal[
    "attempt_failed",
    "baseline",
    "canceled",
    "created",
    "manual_retry",
    "paused",
    "published",
    "rescheduled",
    "resumed",
    "skipped",
]

MANAGED_REALTIME_SCHEDULE_HISTORY_EVENT_EVENT_VALUES: set[ManagedRealtimeScheduleHistoryEventEvent] = {
    "attempt_failed",
    "baseline",
    "canceled",
    "created",
    "manual_retry",
    "paused",
    "published",
    "rescheduled",
    "resumed",
    "skipped",
}


def check_managed_realtime_schedule_history_event_event(value: str) -> ManagedRealtimeScheduleHistoryEventEvent:
    if value in MANAGED_REALTIME_SCHEDULE_HISTORY_EVENT_EVENT_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {MANAGED_REALTIME_SCHEDULE_HISTORY_EVENT_EVENT_VALUES!r}"
    )
