from typing import Literal

ListEnvironmentCacheSchedulesStatus = Literal["cancelled", "executed", "executing", "failed", "scheduled"]

LIST_ENVIRONMENT_CACHE_SCHEDULES_STATUS_VALUES: set[ListEnvironmentCacheSchedulesStatus] = {
    "cancelled",
    "executed",
    "executing",
    "failed",
    "scheduled",
}


def check_list_environment_cache_schedules_status(value: str) -> ListEnvironmentCacheSchedulesStatus:
    if value in LIST_ENVIRONMENT_CACHE_SCHEDULES_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {LIST_ENVIRONMENT_CACHE_SCHEDULES_STATUS_VALUES!r}")
