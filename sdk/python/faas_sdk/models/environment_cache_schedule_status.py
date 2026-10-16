from typing import Literal

EnvironmentCacheScheduleStatus = Literal["cancelled", "executed", "executing", "failed", "scheduled"]

ENVIRONMENT_CACHE_SCHEDULE_STATUS_VALUES: set[EnvironmentCacheScheduleStatus] = {
    "cancelled",
    "executed",
    "executing",
    "failed",
    "scheduled",
}


def check_environment_cache_schedule_status(value: str) -> EnvironmentCacheScheduleStatus:
    if value in ENVIRONMENT_CACHE_SCHEDULE_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ENVIRONMENT_CACHE_SCHEDULE_STATUS_VALUES!r}")
