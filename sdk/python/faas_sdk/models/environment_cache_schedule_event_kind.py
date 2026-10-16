from typing import Literal

EnvironmentCacheScheduleEventKind = Literal["accepted", "cancelled", "executed", "failed", "rescheduled"]

ENVIRONMENT_CACHE_SCHEDULE_EVENT_KIND_VALUES: set[EnvironmentCacheScheduleEventKind] = {
    "accepted",
    "cancelled",
    "executed",
    "failed",
    "rescheduled",
}


def check_environment_cache_schedule_event_kind(value: str) -> EnvironmentCacheScheduleEventKind:
    if value in ENVIRONMENT_CACHE_SCHEDULE_EVENT_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ENVIRONMENT_CACHE_SCHEDULE_EVENT_KIND_VALUES!r}")
