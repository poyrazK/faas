from typing import Literal

EnvironmentCacheScheduleEventStatus = Literal["cancelled", "executed", "failed", "scheduled"]

ENVIRONMENT_CACHE_SCHEDULE_EVENT_STATUS_VALUES: set[EnvironmentCacheScheduleEventStatus] = {
    "cancelled",
    "executed",
    "failed",
    "scheduled",
}


def check_environment_cache_schedule_event_status(value: str) -> EnvironmentCacheScheduleEventStatus:
    if value in ENVIRONMENT_CACHE_SCHEDULE_EVENT_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ENVIRONMENT_CACHE_SCHEDULE_EVENT_STATUS_VALUES!r}")
